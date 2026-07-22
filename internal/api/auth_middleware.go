package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
)

const maxBearerTokenBytes = 16 << 10

func requireAccessToken(verifier auth.TokenVerifier, audit auditLogger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		rawToken, ok := bearerToken(request.Header.Get("Authorization"))
		if !ok || len(rawToken) > maxBearerTokenBytes {
			audit.event(request.Context(), "auth_rejected",
				slog.Int("status", http.StatusUnauthorized),
				slog.String("code", "unauthorized"),
			)
			writeUnauthorized(response)
			return
		}
		identity, err := verifier.Verify(request.Context(), rawToken)
		if err != nil || identity.Subject == "" {
			audit.event(request.Context(), "auth_rejected",
				slog.Int("status", http.StatusUnauthorized),
				slog.String("code", "unauthorized"),
			)
			writeUnauthorized(response)
			return
		}
		ctx := auth.WithBearerToken(request.Context(), rawToken)
		ctx = auth.WithIdentity(ctx, identity)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func bearerToken(authorization string) (string, bool) {
	fields := strings.Fields(authorization)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || fields[1] == "" {
		return "", false
	}
	return fields[1], true
}

func writeUnauthorized(response http.ResponseWriter) {
	response.Header().Set("WWW-Authenticate", "Bearer")
	writeJSONError(response, http.StatusUnauthorized, "unauthorized", "a valid OpenSVC access token is required")
}
