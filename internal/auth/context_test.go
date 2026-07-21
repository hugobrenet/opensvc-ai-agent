package auth

import (
	"context"
	"testing"
)

func TestBearerTokenContext(t *testing.T) {
	if token, ok := BearerTokenFromContext(context.Background()); ok || token != "" {
		t.Fatalf("empty context returned token %q, present=%v", token, ok)
	}

	ctx := WithBearerToken(context.Background(), "delegated-token")
	if token, ok := BearerTokenFromContext(ctx); !ok || token != "delegated-token" {
		t.Fatalf("got token %q, present=%v", token, ok)
	}
}

func TestEmptyBearerTokenIsAbsent(t *testing.T) {
	ctx := WithBearerToken(context.Background(), "")
	if token, ok := BearerTokenFromContext(ctx); ok || token != "" {
		t.Fatalf("empty token returned as %q, present=%v", token, ok)
	}
}
