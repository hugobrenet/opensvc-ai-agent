package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
)

func TestRequireAccessTokenRemovesAuthorizationHeaderAndPreservesContext(t *testing.T) {
	const token = "delegated-token"
	verifier := tokenVerifierFunc(func(_ context.Context, got string) (auth.Identity, error) {
		if got != token {
			t.Fatalf("verifier token = %q", got)
		}
		return auth.Identity{Subject: "alice", Issuer: "node-a"}, nil
	})
	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		called = true
		if got := request.Header.Get("Authorization"); got != "" {
			t.Fatalf("downstream Authorization header = %q", got)
		}
		if got, ok := auth.BearerTokenFromContext(request.Context()); !ok || got != token {
			t.Fatalf("delegated context token = %q, %v", got, ok)
		}
		if identity, ok := auth.IdentityFromContext(request.Context()); !ok || identity.Subject != "alice" {
			t.Fatalf("verified identity = %+v, %v", identity, ok)
		}
	})
	handler := requireAccessToken(verifier, auditLogger{logger: discardAuditLogger()}, next)
	request := httptest.NewRequest(http.MethodPost, "/v1/ask", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if !called {
		t.Fatal("downstream handler was not called")
	}
}
