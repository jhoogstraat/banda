package api

import (
	"context"

	"connectrpc.com/connect/v2"
)

type userKey struct{}

// WithUser returns ctx carrying the authenticated user id. Auth interceptors
// call it; handlers read it with user.
func WithUser(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userKey{}, id)
}

func user(ctx context.Context) string {
	id, _ := ctx.Value(userKey{}).(string)
	return id
}

// DevAuth trusts the X-Banda-User header and defaults to "dev".
// ponytail: local development only; OIDC replaces it when configured.
func DevAuth() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			id := "dev"
			if info, ok := connect.CallInfoForServerContext(ctx); ok && info.RequestHeader().Get("X-Banda-User") != "" {
				id = info.RequestHeader().Get("X-Banda-User")
			}
			return next(WithUser(ctx, id), spec, stream)
		}
	}
}
