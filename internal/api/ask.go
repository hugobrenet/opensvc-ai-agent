package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
)

const (
	maxAskRequestBytes = 64 << 10
	maxPromptBytes     = 32 << 10
	maxAskStreamBytes  = 16 << 20
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

func serveAsk(asker Asker) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		askRequest, status, apiError := decodeAskRequest(response, request)
		if apiError != nil {
			writeJSONError(response, status, apiError.Code, apiError.Message)
			return
		}
		flusher, ok := response.(http.Flusher)
		if !ok {
			writeJSONError(response, http.StatusInternalServerError, "streaming_unavailable", "response streaming is unavailable")
			return
		}

		response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.WriteHeader(http.StatusOK)
		flusher.Flush()

		writeFailed := false
		completed := false
		var streamBytes int64
		err := asker.Ask(request.Context(), askRequest.Prompt, func(event agent.Event) error {
			streamEvent := newAskEvent(event)
			if err := writeSSE(response, flusher, &streamBytes, streamEvent); err != nil {
				writeFailed = true
				return err
			}
			if event.Type == agent.EventCompleted {
				completed = true
			}
			return nil
		})
		if err != nil && !completed && !writeFailed && request.Context().Err() == nil {
			_ = writeSSE(response, flusher, &streamBytes, AskEvent{
				Type:    "error",
				Code:    "agent_failed",
				Message: "the agent could not complete the request",
			})
		}
	}
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
