package auth

import "context"

type bearerTokenContextKey struct{}

type contextWithoutBearerToken struct {
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

// WithoutBearerToken returns a context that preserves cancellation, deadlines,
// and unrelated values while hiding the delegated OpenSVC access JWT.
func WithoutBearerToken(ctx context.Context) context.Context {
	return contextWithoutBearerToken{Context: ctx}
}

func (c contextWithoutBearerToken) Value(key any) any {
	if _, ok := key.(bearerTokenContextKey); ok {
		return nil
	}
	return c.Context.Value(key)
}
