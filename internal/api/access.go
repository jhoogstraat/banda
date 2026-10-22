package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"

	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

// Who may do what: everyone signed in reads every thread (events, files,
// preview, attachments) and posts into it; the state machine decides what a
// post does. Only the owner changes how a thread runs or uses its
// environment (thread.State.CheckOwner) and answers its agent's permission
// prompts, unless they trust teammates (thread.State.Trusts). Runners and the
// MCP server act as their thread, by its runner token.

// owned returns the thread if the caller owns it.
func (s *Service) owned(ctx context.Context, id string) (store.Thread, error) {
	t, err := s.store.Get(ctx, id)
	if err == nil {
		err = t.CheckOwner(user(ctx))
	}
	return t, err
}

// runnerThread returns the thread if token is its runner token.
func (s *Service) runnerThread(ctx context.Context, id, token string) (store.Thread, error) {
	t, err := s.store.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && (t.RunnerHash == "" || subtle.ConstantTimeCompare([]byte(t.RunnerHash), []byte(store.TokenHash(token))) != 1) {
		return t, connect.NewError(connect.CodeUnauthenticated, "unknown thread or bad token")
	}
	return t, err
}

// Errors maps what handlers return to what clients see; see publicError.
// It needs the user, so it goes after the auth interceptor.
func Errors() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return publicError(ctx, spec.Procedure, next(ctx, spec, stream))
		}
	}
}

// codes are the errors clients may see, with their code.
var codes = map[error]connect.Code{
	store.ErrNotFound:     connect.CodeNotFound,
	store.ErrNoAttachment: connect.CodeNotFound,
	thread.ErrForbidden:   connect.CodePermissionDenied,
	thread.ErrNotReady:    connect.CodeFailedPrecondition,
	thread.ErrNoRequest:   connect.CodeFailedPrecondition,
	thread.ErrNoHandoff:   connect.CodeFailedPrecondition,
	thread.ErrHandoff:     connect.CodeInvalidArgument,
	thread.ErrBusy:        connect.CodeFailedPrecondition,

	context.Canceled:         connect.CodeCanceled, // the client went away
	context.DeadlineExceeded: connect.CodeDeadlineExceeded,
}

// publicError passes a *connect.Error on and gives a known error its code.
// Anything else, e.g. a database error, is logged with the call (op) and
// becomes a generic CodeInternal.
func publicError(ctx context.Context, op string, err error) error {
	var ce *connect.Error
	if err == nil || errors.As(err, &ce) {
		return err
	}
	for known, code := range codes {
		if errors.Is(err, known) {
			return connect.NewError(code, known.Error())
		}
	}
	slog.Error("internal error", "op", op, "user", user(ctx), "err", err)
	return connect.NewError(connect.CodeInternal, "internal error")
}
