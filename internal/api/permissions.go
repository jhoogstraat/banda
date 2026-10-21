package api

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

// Permission prompts: the runner forwards Claude Code's can_use_tool requests
// as PermissionRequested events and holds them until the answer comes back
// here as a PermissionCommand. The thread remembers which are pending, so the
// inbox can show it as needing input. Only the owner answers (their machine,
// their subscription), or anyone once they trust teammates; a plan only the
// owner approves, since that changes how the agent works from then on.

const exitPlanMode = "ExitPlanMode"

var (
	permissionModeToProto = map[string]bandav1.PermissionMode{
		"bypass":       bandav1.PermissionMode_PERMISSION_MODE_BYPASS,
		"ask":          bandav1.PermissionMode_PERMISSION_MODE_ASK,
		"accept_edits": bandav1.PermissionMode_PERMISSION_MODE_ACCEPT_EDITS,
		"plan":         bandav1.PermissionMode_PERMISSION_MODE_PLAN,
	}
	permissionModeFromProto = func() map[bandav1.PermissionMode]string {
		m := map[bandav1.PermissionMode]string{}
		for k, v := range permissionModeToProto {
			m[v] = k
		}
		return m
	}()
	errNoPermission = connect.NewError(connect.CodeFailedPrecondition, "this permission request is no longer pending")
)

// newPermissionMode is the mode a new thread is created in: "" (the default)
// when unspecified.
func newPermissionMode(m bandav1.PermissionMode) (string, error) {
	if m == bandav1.PermissionMode_PERMISSION_MODE_UNSPECIFIED {
		return "", nil
	}
	mode, ok := permissionModeFromProto[m]
	if !ok {
		return "", connect.NewError(connect.CodeInvalidArgument, "unknown permission mode")
	}
	return mode, nil
}

func (s *Service) PermissionSetMode(ctx context.Context, req *bandav1.PermissionSetModeRequest) (*bandav1.PermissionSetModeResponse, error) {
	mode, ok := permissionModeFromProto[req.Mode]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, "unknown permission mode")
	}
	t, events, err := s.store.Update(ctx, req.ThreadId, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		if err := t.CheckOwner(user(ctx)); err != nil {
			return nil, err
		}
		if t.PermissionMode == mode {
			return nil, nil
		}
		t.PermissionMode = mode
		return []*bandav1.Event{{Actor: t.OwnerID, Kind: &bandav1.Event_PermissionModeChanged{PermissionModeChanged: &bandav1.PermissionModeChanged{Mode: req.Mode}}}}, nil
	})
	if err != nil {
		return nil, err
	}
	s.broadcast(req.ThreadId, events)
	s.configure(t) // not running: the runner gets it when it is ready
	return &bandav1.PermissionSetModeResponse{}, nil
}

func (s *Service) PermissionAnswer(ctx context.Context, req *bandav1.PermissionAnswerRequest) (*bandav1.PermissionAnswerResponse, error) {
	d := req.Decision
	if d != bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW && d != bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW_ALWAYS &&
		d != bandav1.PermissionDecision_PERMISSION_DECISION_DENY {
		return nil, connect.NewError(connect.CodeInvalidArgument, "decision must be allow, allow always or deny")
	}
	by, id := user(ctx), req.ThreadId
	s.mu.Lock()
	running := s.runners[id] != nil
	s.mu.Unlock()
	if !running { // its request died with the environment; reconnecting marks it lost
		return nil, errNotRunning
	}
	var cmd *bandav1.PermissionCommand
	_, events, err := s.store.Update(ctx, id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		res, c, err := answerPermission(t, by, req)
		cmd = c
		return []*bandav1.Event{{Actor: by, Kind: &bandav1.Event_PermissionResolved{PermissionResolved: res}}}, err
	})
	if err != nil {
		return nil, err
	}
	s.broadcast(id, events)
	s.sendRunner(id, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Permission{Permission: cmd}})
	return &bandav1.PermissionAnswerResponse{}, nil
}

// answerPermission applies an answer by `by` to t: who may answer, a plan approval
// switching the mode, a tool allowed from now on.
func answerPermission(t *store.Thread, by string, req *bandav1.PermissionAnswerRequest) (*bandav1.PermissionResolved, *bandav1.PermissionCommand, error) {
	d := req.Decision
	tool, ok := t.Permissions[req.RequestId]
	if !ok {
		return nil, nil, errNoPermission
	}
	plan := tool == exitPlanMode
	if plan && t.CheckOwner(by) != nil || !t.Trusts(by) {
		return nil, nil, thread.ErrForbidden
	}
	res := &bandav1.PermissionResolved{Id: req.RequestId, Decision: d, Message: req.Message}
	switch {
	case plan && d != bandav1.PermissionDecision_PERMISSION_DECISION_DENY:
		mode, ok := permissionModeFromProto[req.Mode]
		if !ok || mode == "plan" {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument, "approving a plan needs the mode to work in")
		}
		t.PermissionMode, res.Mode, res.Decision = mode, req.Mode, bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW
	case d == bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW_ALWAYS && !slices.Contains(t.AllowedTools, tool):
		t.AllowedTools = append(t.AllowedTools, tool)
	}
	delete(t.Permissions, req.RequestId)
	return res, &bandav1.PermissionCommand{RequestId: req.RequestId, Decision: res.Decision, Message: req.Message, Mode: res.Mode, AllowedTools: t.AllowedTools}, nil
}

// configure sends the thread's permission settings to its runner, if any.
func (s *Service) configure(t store.Thread) {
	s.sendRunner(t.ID, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Permission{Permission: &bandav1.PermissionCommand{
		Mode: permissionModeToProto[t.PermissionMode], AllowedTools: t.AllowedTools}}})
}

// permissionEvent records a request from the runner, or its withdrawal.
func (s *Service) permissionEvent(ctx context.Context, id string, e *bandav1.Event) {
	_, events, err := s.store.Update(ctx, id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		if t.Permissions == nil {
			t.Permissions = map[string]string{}
		}
		if r := e.GetPermissionRequested(); r != nil {
			t.Permissions[r.Id] = r.ToolName
		} else if _, ok := t.Permissions[e.GetPermissionResolved().GetId()]; ok {
			delete(t.Permissions, e.GetPermissionResolved().Id)
		} else {
			return nil, errNoPermission // answered already
		}
		return []*bandav1.Event{e}, nil
	})
	if err != nil && !errors.Is(err, errNoPermission) {
		slog.Error("store permission event", "thread", id, "err", err)
	}
	s.broadcast(id, events)
}

// settlePermissions resolves every pending request: CANCELED when the turn
// ended without them, LOST when the environment went away and the request
// with it.
func (s *Service) settlePermissions(ctx context.Context, id string, d bandav1.PermissionDecision) {
	if t, err := s.store.Get(ctx, id); err != nil || len(t.Permissions) == 0 {
		return
	}
	_, events, err := s.store.Update(ctx, id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		var out []*bandav1.Event
		for _, rid := range slices.Sorted(maps.Keys(t.Permissions)) {
			out = append(out, &bandav1.Event{Actor: "system", Kind: &bandav1.Event_PermissionResolved{PermissionResolved: &bandav1.PermissionResolved{Id: rid, Decision: d}}})
		}
		t.Permissions = nil
		return out, nil
	})
	if err != nil {
		slog.Error("settle permissions", "thread", id, "err", err)
	}
	s.broadcast(id, events)
}
