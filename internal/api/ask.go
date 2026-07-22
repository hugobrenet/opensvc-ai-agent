package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
)

const (
	maxAskRequestBytes = 64 << 10
	maxPromptBytes     = 32 << 10
	maxAskStreamBytes  = 16 << 20
	askWriteTimeout    = 15 * time.Second
)

type Asker interface {
	Ask(context.Context, string, agent.EmitFunc) error
}

type AskRequest struct {
	Prompt string `json:"prompt"`
}

type AskUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type AskEvent struct {
	Type         string    `json:"type"`
	Iteration    int       `json:"iteration,omitempty"`
	TextDelta    string    `json:"text_delta,omitempty"`
	ToolName     string    `json:"tool_name,omitempty"`
	ToolError    *bool     `json:"tool_error,omitempty"`
	Usage        *AskUsage `json:"usage,omitempty"`
	FinishReason string    `json:"finish_reason,omitempty"`
	Code         string    `json:"code,omitempty"`
	Message      string    `json:"message,omitempty"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func serveAsk(asker Asker, limiter *askLimiter, audit auditLogger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		askRequest, status, apiError := decodeAskRequest(response, request)
		if apiError != nil {
			audit.event(request.Context(), "ask_rejected",
				slog.Int("status", status),
				slog.String("code", apiError.Code),
			)
			writeJSONError(response, status, apiError.Code, apiError.Message)
			return
		}
		if !limiter.tryAcquire() {
			audit.event(request.Context(), "ask_rejected",
				slog.Int("status", http.StatusTooManyRequests),
				slog.String("code", "too_many_requests"),
			)
			response.Header().Set("Retry-After", "1")
			writeJSONError(response, http.StatusTooManyRequests, "too_many_requests", "too many agent requests are already running")
			return
		}
		defer limiter.release()
		flusher, ok := response.(http.Flusher)
		if !ok {
			audit.event(request.Context(), "ask_rejected",
				slog.Int("status", http.StatusInternalServerError),
				slog.String("code", "streaming_unavailable"),
			)
			writeJSONError(response, http.StatusInternalServerError, "streaming_unavailable", "response streaming is unavailable")
			return
		}
		if err := setAskWriteDeadline(response); err != nil {
			audit.event(request.Context(), "ask_rejected",
				slog.Int("status", http.StatusInternalServerError),
				slog.String("code", "streaming_unavailable"),
			)
			writeJSONError(response, http.StatusInternalServerError, "streaming_unavailable", "response streaming is unavailable")
			return
		}
		defer clearAskWriteDeadline(response)

		response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.WriteHeader(http.StatusOK)
		flusher.Flush()
		startedAt := time.Now()
		audit.event(request.Context(), "ask_started", slog.Int("status", http.StatusOK))

		writeFailed := false
		completed := false
		finishReason := ""
		lastIteration := 0
		toolCalls := 0
		var usage AskUsage
		var activeToolName string
		var activeToolStartedAt time.Time
		var streamBytes int64
		recordEvent := func(event agent.Event) {
			switch event.Type {
			case agent.EventToolStarted:
				toolCalls++
				activeToolName = event.ToolName
				activeToolStartedAt = time.Now()
				audit.event(request.Context(), "tool_started",
					slog.String("tool_name", event.ToolName),
					slog.Int("iteration", event.Iteration),
				)
			case agent.EventToolFinished:
				duration := int64(0)
				if activeToolName == event.ToolName && !activeToolStartedAt.IsZero() {
					duration = time.Since(activeToolStartedAt).Milliseconds()
				}
				attributes := []slog.Attr{
					slog.String("tool_name", event.ToolName),
					slog.Int("iteration", event.Iteration),
					slog.Bool("tool_error", event.ToolError),
					slog.Int64("duration_ms", duration),
				}
				if event.ToolError {
					attributes = append(attributes, slog.String("code", "tool_error"))
				}
				audit.event(request.Context(), "tool_finished", attributes...)
				activeToolName = ""
				activeToolStartedAt = time.Time{}
			case agent.EventUsage:
				usage.InputTokens = saturatingAdd(usage.InputTokens, event.Usage.InputTokens)
				usage.OutputTokens = saturatingAdd(usage.OutputTokens, event.Usage.OutputTokens)
				usage.TotalTokens = saturatingAdd(usage.TotalTokens, event.Usage.TotalTokens)
				audit.event(request.Context(), "llm_usage",
					slog.Int("iteration", event.Iteration),
					slog.Int64("input_tokens", event.Usage.InputTokens),
					slog.Int64("output_tokens", event.Usage.OutputTokens),
					slog.Int64("total_tokens", event.Usage.TotalTokens),
				)
			case agent.EventCompleted:
				completed = true
				finishReason = string(event.FinishReason)
			}
		}
		err := asker.Ask(request.Context(), askRequest.Prompt, func(event agent.Event) error {
			lastIteration = max(lastIteration, event.Iteration)
			if event.Type != agent.EventToolStarted {
				recordEvent(event)
			}
			streamEvent := newAskEvent(event)
			if err := setAskWriteDeadline(response); err != nil {
				writeFailed = true
				return err
			}
			if err := writeSSE(response, flusher, &streamBytes, streamEvent); err != nil {
				writeFailed = true
				return err
			}
			if event.Type == agent.EventToolStarted {
				recordEvent(event)
			}
			return nil
		})
		failureCode := askFailureCode(err, completed, writeFailed, request.Context().Err())
		if activeToolName != "" && failureCode == "" {
			failureCode = "agent_incomplete"
		}
		if activeToolName != "" {
			audit.event(request.Context(), "tool_finished",
				slog.String("tool_name", activeToolName),
				slog.Int("iteration", lastIteration),
				slog.Bool("tool_error", true),
				slog.Int64("duration_ms", time.Since(activeToolStartedAt).Milliseconds()),
				slog.String("code", failureCode),
			)
		}
		terminalAttributes := []slog.Attr{
			slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
			slog.Int("iterations", lastIteration),
			slog.Int("tool_calls", toolCalls),
			slog.Int64("input_tokens", usage.InputTokens),
			slog.Int64("output_tokens", usage.OutputTokens),
			slog.Int64("total_tokens", usage.TotalTokens),
		}
		if failureCode == "" {
			terminalAttributes = append(terminalAttributes, slog.String("finish_reason", finishReason))
			audit.event(request.Context(), "ask_completed", terminalAttributes...)
		} else {
			terminalAttributes = append(terminalAttributes, slog.String("code", failureCode))
			audit.event(request.Context(), "ask_failed", terminalAttributes...)
		}
		if err != nil && !completed && !writeFailed && request.Context().Err() == nil {
			code := "agent_failed"
			message := "the agent could not complete the request"
			if errors.Is(err, context.DeadlineExceeded) {
				code = "request_timeout"
				message = "the agent request timed out"
			}
			if deadlineErr := setAskWriteDeadline(response); deadlineErr != nil {
				return
			}
			_ = writeSSE(response, flusher, &streamBytes, AskEvent{
				Type:    "error",
				Code:    code,
				Message: message,
			})
		}
	}
}

func askFailureCode(err error, completed bool, writeFailed bool, contextErr error) string {
	if writeFailed {
		return "stream_write_failed"
	}
	if errors.Is(contextErr, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "request_timeout"
	}
	if contextErr != nil || errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	if err != nil {
		if completed {
			return "agent_cleanup_failed"
		}
		return "agent_failed"
	}
	if !completed {
		return "agent_incomplete"
	}
	return ""
}

func saturatingAdd(current int64, value int64) int64 {
	if value < 0 {
		return current
	}
	if value > math.MaxInt64-current {
		return math.MaxInt64
	}
	return current + value
}

func setAskWriteDeadline(response http.ResponseWriter) error {
	err := http.NewResponseController(response).SetWriteDeadline(time.Now().Add(askWriteTimeout))
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("set API stream write deadline: %w", err)
	}
	return nil
}

func clearAskWriteDeadline(response http.ResponseWriter) {
	_ = http.NewResponseController(response).SetWriteDeadline(time.Time{})
}

func decodeAskRequest(response http.ResponseWriter, request *http.Request) (AskRequest, int, *APIError) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return AskRequest{}, http.StatusUnsupportedMediaType, &APIError{Code: "unsupported_media_type", Message: "Content-Type must be application/json"}
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxAskRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var askRequest AskRequest
	if err := decoder.Decode(&askRequest); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return AskRequest{}, http.StatusRequestEntityTooLarge, &APIError{Code: "request_too_large", Message: "request body is too large"}
		}
		return AskRequest{}, http.StatusBadRequest, &APIError{Code: "invalid_request", Message: "request body must be a JSON object containing a prompt"}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return AskRequest{}, http.StatusBadRequest, &APIError{Code: "invalid_request", Message: "request body must contain one JSON object"}
	}
	if strings.TrimSpace(askRequest.Prompt) == "" {
		return AskRequest{}, http.StatusBadRequest, &APIError{Code: "invalid_prompt", Message: "prompt must not be empty"}
	}
	if len(askRequest.Prompt) > maxPromptBytes {
		return AskRequest{}, http.StatusRequestEntityTooLarge, &APIError{Code: "prompt_too_large", Message: "prompt is too large"}
	}
	return askRequest, 0, nil
}

func newAskEvent(event agent.Event) AskEvent {
	streamEvent := AskEvent{Type: string(event.Type), Iteration: event.Iteration}
	switch event.Type {
	case agent.EventTextDelta:
		streamEvent.TextDelta = event.TextDelta
	case agent.EventToolStarted:
		streamEvent.ToolName = event.ToolName
	case agent.EventToolFinished:
		streamEvent.ToolName = event.ToolName
		toolError := event.ToolError
		streamEvent.ToolError = &toolError
	case agent.EventUsage:
		streamEvent.Usage = &AskUsage{
			InputTokens:  event.Usage.InputTokens,
			OutputTokens: event.Usage.OutputTokens,
			TotalTokens:  event.Usage.TotalTokens,
		}
	case agent.EventCompleted:
		streamEvent.FinishReason = string(event.FinishReason)
	}
	return streamEvent
}

func writeSSE(response io.Writer, flusher http.Flusher, streamBytes *int64, event AskEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode API stream event: %w", err)
	}
	frame := fmt.Appendf(nil, "event: %s\ndata: %s\n\n", event.Type, data)
	if *streamBytes+int64(len(frame)) > maxAskStreamBytes {
		return fmt.Errorf("API stream exceeds %d bytes", maxAskStreamBytes)
	}
	written, err := response.Write(frame)
	*streamBytes += int64(written)
	if err != nil {
		return fmt.Errorf("write API stream event: %w", err)
	}
	if written != len(frame) {
		return fmt.Errorf("write API stream event: %w", io.ErrShortWrite)
	}
	flusher.Flush()
	return nil
}

func writeJSONError(response http.ResponseWriter, status int, code string, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(ErrorResponse{Error: APIError{Code: code, Message: message}})
}
