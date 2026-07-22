package auth

import (
	"context"
	"testing"
	"time"
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

func TestWithoutAuthenticationPreservesUnrelatedContext(t *testing.T) {
	type unrelatedKey struct{}
	base, cancel := context.WithCancel(context.WithValue(t.Context(), unrelatedKey{}, "value"))
	ctx := WithBearerToken(base, "jwt-marker")
	ctx = WithIdentity(ctx, Identity{Subject: "alice", Issuer: "node-a", Grants: []string{"guest"}, ExpiresAt: time.Now().Add(time.Hour)})
	sanitized := WithoutAuthentication(ctx)

	if _, ok := BearerTokenFromContext(sanitized); ok {
		t.Fatal("sanitized context contains bearer token")
	}
	if _, ok := IdentityFromContext(sanitized); ok {
		t.Fatal("sanitized context contains verified identity")
	}
	if got := sanitized.Value(unrelatedKey{}); got != "value" {
		t.Fatalf("unrelated context value = %v, want value", got)
	}
	cancel()
	if sanitized.Err() != context.Canceled {
		t.Fatalf("sanitized context error = %v, want context canceled", sanitized.Err())
	}
}

func TestIdentityContextCopiesGrants(t *testing.T) {
	grants := []string{"guest"}
	ctx := WithIdentity(context.Background(), Identity{Subject: "alice", Issuer: "node-a", Grants: grants})
	grants[0] = "root"
	identity, ok := IdentityFromContext(ctx)
	if !ok || identity.Subject != "alice" || identity.Grants[0] != "guest" {
		t.Fatalf("unexpected identity %+v, present=%v", identity, ok)
	}
	identity.Grants[0] = "root"
	again, _ := IdentityFromContext(ctx)
	if again.Grants[0] != "guest" {
		t.Fatal("identity grants were mutated through returned value")
	}
}
