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

func TestWithoutBearerTokenPreservesContextExceptToken(t *testing.T) {
	type unrelatedKey struct{}
	base, cancel := context.WithCancel(context.WithValue(t.Context(), unrelatedKey{}, "value"))
	sanitized := WithoutBearerToken(WithBearerToken(base, "jwt-marker"))

	if _, ok := BearerTokenFromContext(sanitized); ok {
		t.Fatal("sanitized context contains bearer token")
	}
	if got := sanitized.Value(unrelatedKey{}); got != "value" {
		t.Fatalf("unrelated context value = %v, want value", got)
	}
	cancel()
	if sanitized.Err() != context.Canceled {
		t.Fatalf("sanitized context error = %v, want context canceled", sanitized.Err())
	}
}
