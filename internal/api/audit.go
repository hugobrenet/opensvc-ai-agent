package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
)

const (
	requestIDHeader       = "X-Request-ID"
	requestIDBytes        = 16
	maxAuditIdentityRunes = 256
	maxAuditIDRunes       = 128
)

type requestIDContextKey struct{}

type auditLogger struct {
	logger *slog.Logger
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID, err := newRequestID()
		if err != nil {
			writeJSONError(response, http.StatusInternalServerError, "request_id_failed", "the request could not be processed")
			return
		}
		response.Header().Set(requestIDHeader, requestID)
		ctx := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func newRequestID() (string, error) {
	data := make([]byte, requestIDBytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func (a auditLogger) event(ctx context.Context, event string, attributes ...slog.Attr) {
	base := []slog.Attr{
		slog.String("event", event),
		slog.String("request_id", requestIDFromContext(ctx)),
	}
	if identity, ok := auth.IdentityFromContext(ctx); ok {
		base = append(base,
			slog.String("subject", boundedAuditIdentity(identity.Subject)),
			slog.String("issuer", boundedAuditIdentity(identity.Issuer)),
		)
	}
	base = append(base, attributes...)
	a.logger.LogAttrs(ctx, slog.LevelInfo, "agent audit", base...)
}

func boundedAuditIdentity(value string) string {
	return boundedAuditText(value, maxAuditIdentityRunes)
}

func boundedAuditID(value string) string {
	return boundedAuditText(value, maxAuditIDRunes)
}

func boundedAuditText(value string, maximum int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum]) + "…"
}
