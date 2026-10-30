package api

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
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
	return s.terminal(ctx, id, &bandav1.TerminalOpen{Cols: req.Cols, Rows: req.Rows, Command: req.Command}, stream.Send)
}

// terminal opens a terminal on the runner connected as key and streams its
// output until it exits or the client goes away.
func (s *Service) terminal(ctx context.Context, key string, open *bandav1.TerminalOpen, send func(*bandav1.OpenTerminalResponse) error) error {
	if err := s.waitRunner(ctx, key, func(why string) error { return send(&bandav1.OpenTerminalResponse{Waiting: why}) }); err != nil {
		return err
	}

	// A new terminal replaces the previous one, whose stream then ends.
	ch := make(chan *bandav1.TerminalOutput, 64)
	s.mu.Lock()
	if old := s.terminals[key]; old != nil {
		close(old)
	}
	s.terminals[key] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		mine := s.terminals[key] == ch
		if mine {
			delete(s.terminals, key)
		}
		s.mu.Unlock()
		if mine { // the client went away or the terminal ended
			s.sendRunner(key, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalClose{TerminalClose: &bandav1.TerminalClose{}}})
		}
	}()
	if !s.sendRunner(key, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalOpen{TerminalOpen: open}}) {
		return errNotRunning
	}

	tick := time.NewTicker(s.Keepalive)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := send(&bandav1.OpenTerminalResponse{}); err != nil {
				return err
			}
		case o, ok := <-ch:
			if !ok {
				return nil
			}
			if err := send(&bandav1.OpenTerminalResponse{Data: o.Data, Exited: o.Exited}); err != nil || o.Exited {
				return err
			}
		}
	}
}

// closeTerminal ends key's terminal stream, if any. s.mu must be held.
func (s *Service) closeTerminal(key string) {
	if term := s.terminals[key]; term != nil {
		close(term)
		delete(s.terminals, key)
	}
}

func (s *Service) WriteTerminal(ctx context.Context, req *bandav1.WriteTerminalRequest) (*bandav1.WriteTerminalResponse, error) {
	if _, err := s.owned(ctx, req.ThreadId); err != nil {
		return nil, err
	}
	return &bandav1.WriteTerminalResponse{}, s.toTerminal(req.ThreadId, terminalInput(req.Data))
}

func (s *Service) ResizeTerminal(ctx context.Context, req *bandav1.ResizeTerminalRequest) (*bandav1.ResizeTerminalResponse, error) {
	if _, err := s.owned(ctx, req.ThreadId); err != nil {
		return nil, err
	}
	return &bandav1.ResizeTerminalResponse{}, s.toTerminal(req.ThreadId, terminalResize(req.Cols, req.Rows))
}

func terminalInput(data []byte) *bandav1.ConnectResponse {
	return &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalInput{TerminalInput: &bandav1.TerminalInput{Data: data}}}
}

func terminalResize(cols, rows uint32) *bandav1.ConnectResponse {
	return &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalResize{TerminalResize: &bandav1.TerminalResize{Cols: cols, Rows: rows}}}
}

func (s *Service) toTerminal(key string, msg *bandav1.ConnectResponse) error {
	if !s.sendRunner(key, msg) {
		return errNotRunning
	}
	return nil
}

// waitRunner waits for the runner connected as key to connect, telling
// waiting what its environment waits on every StartPoll (see
// sandbox.Waiting). It fails with the reason once the environment can't
// start, or StartDeadline after it began waiting: the thread's own start ends
// by its watch, and a login environment has no other.
func (s *Service) waitRunner(ctx context.Context, key string, waiting func(string) error) error {
	deadline, last := time.Now().Add(s.StartDeadline), ""
	for next := time.Now(); ; {
		s.mu.Lock()
		_, ok := s.runners[key]
		s.mu.Unlock()
		if ok {
			return nil
		}
		if time.Now().After(next) {
			next = time.Now().Add(s.StartPoll)
			var why string
			var failed bool
			var err error
			if u, login := strings.CutPrefix(key, loginPrefix); login {
				why, failed, err = s.sandboxes.WaitingLogin(ctx, u)
			} else {
				why, failed, err = s.sandboxes.Waiting(ctx, key)
			}
			if err != nil {
				return err
			}
			if failed {
				return connect.NewError(connect.CodeFailedPrecondition, why)
			}
			last = or(why, last)
			if time.Now().After(deadline) {
				return connect.NewError(connect.CodeFailedPrecondition, s.tooLong(last))
			}
			if err := waiting(or(why, "Starting the environment…")); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return errNotRunning
		case <-time.After(500 * time.Millisecond):
		}
	}
}
