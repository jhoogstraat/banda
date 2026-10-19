package api

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

// OpenTerminal streams the runner's terminal output. Terminal bytes pass
// through here and are never logged or stored.
func (s *Service) OpenTerminal(ctx context.Context, req *bandav1.OpenTerminalRequest, stream bandav1connect.ThreadServiceOpenTerminalServerStream) error {
	id := req.ThreadId
	t, err := s.owned(ctx, id)
	if err != nil {
		return err
	}
	if t.Status == thread.Suspended {
		effect, err := s.apply(ctx, id, thread.Wake{By: user(ctx)})
		if err != nil {
			return err
		}
		s.do(ctx, id, effect)
	}
	if !s.waitRunner(ctx, id) {
		return connect.NewError(connect.CodeUnavailable, "the environment is not running")
	}

	// A new terminal replaces the previous one, whose stream then ends.
	ch := make(chan *bandav1.TerminalOutput, 64)
	s.mu.Lock()
	if old := s.terminals[id]; old != nil {
		close(old)
	}
	s.terminals[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		mine := s.terminals[id] == ch
		if mine {
			delete(s.terminals, id)
		}
		s.mu.Unlock()
		if mine { // the client went away or the terminal ended
			s.sendRunner(id, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalClose{TerminalClose: &bandav1.TerminalClose{}}})
		}
	}()
	if !s.sendRunner(id, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalOpen{
		TerminalOpen: &bandav1.TerminalOpen{Cols: req.Cols, Rows: req.Rows, Command: req.Command}}}) {
		return connect.NewError(connect.CodeUnavailable, "the environment is not running")
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case o, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&bandav1.OpenTerminalResponse{Data: o.Data, Exited: o.Exited}); err != nil || o.Exited {
				return err
			}
		}
	}
}

func (s *Service) WriteTerminal(ctx context.Context, req *bandav1.WriteTerminalRequest) (*bandav1.WriteTerminalResponse, error) {
	return &bandav1.WriteTerminalResponse{}, s.toTerminal(ctx, req.ThreadId,
		&bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalInput{TerminalInput: &bandav1.TerminalInput{Data: req.Data}}})
}

func (s *Service) ResizeTerminal(ctx context.Context, req *bandav1.ResizeTerminalRequest) (*bandav1.ResizeTerminalResponse, error) {
	return &bandav1.ResizeTerminalResponse{}, s.toTerminal(ctx, req.ThreadId,
		&bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalResize{TerminalResize: &bandav1.TerminalResize{Cols: req.Cols, Rows: req.Rows}}})
}

func (s *Service) toTerminal(ctx context.Context, id string, msg *bandav1.ConnectResponse) error {
	if _, err := s.owned(ctx, id); err != nil {
		return err
	}
	if !s.sendRunner(id, msg) {
		return connect.NewError(connect.CodeFailedPrecondition, "the environment is not running")
	}
	return nil
}

// owned returns the thread if the caller owns it.
func (s *Service) owned(ctx context.Context, id string) (store.Thread, error) {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return t, cerr(err)
	}
	if t.OwnerID != user(ctx) {
		return t, cerr(thread.ErrForbidden)
	}
	return t, nil
}

// waitRunner waits for the thread's runner to connect, up to 90 s: enough
// for a suspended environment to resume.
func (s *Service) waitRunner(ctx context.Context, id string) bool {
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
		s.mu.Lock()
		_, ok := s.runners[id]
		s.mu.Unlock()
		if ok {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}
