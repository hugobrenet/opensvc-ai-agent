package auth

import "context"

type bearerTokenContextKey struct{}
type identityContextKey struct{}

type contextWithoutAuthentication struct {
	context.Context
}

// WithBearerToken returns a context carrying a delegated OpenSVC access JWT.
// Callers must keep the returned context scoped to a single agent request.
func WithBearerToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerTokenContextKey{}, token)
}

// BearerTokenFromContext returns the delegated OpenSVC access JWT.
func BearerTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(bearerTokenContextKey{}).(string)
	return token, ok && token != ""
}

// WithIdentity returns a context carrying the verified OpenSVC caller identity.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	identity.Grants = append([]string(nil), identity.Grants...)
	return context.WithValue(ctx, identityContextKey{}, identity)
}

// IdentityFromContext returns a copy of the verified OpenSVC caller identity.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	if !ok || identity.Subject == "" {
		return Identity{}, false
	}
	identity.Grants = append([]string(nil), identity.Grants...)
	return identity, true
}

// WithoutAuthentication returns a context that preserves cancellation,
// deadlines, and unrelated values while hiding OpenSVC authentication data.
func WithoutAuthentication(ctx context.Context) context.Context {
	return contextWithoutAuthentication{Context: ctx}
}

func (c contextWithoutAuthentication) Value(key any) any {
	if _, ok := key.(bearerTokenContextKey); ok {
		return nil
	}
	if _, ok := key.(identityContextKey); ok {
		return nil
	}
	return c.Context.Value(key)
}
