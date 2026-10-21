package api

import (
	"errors"
	"slices"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

func TestResolvePermission(t *testing.T) {
	const (
		allow  = bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW
		always = bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW_ALWAYS
		deny   = bandav1.PermissionDecision_PERMISSION_DECISION_DENY
		edits  = bandav1.PermissionMode_PERMISSION_MODE_ACCEPT_EDITS
	)
	thr := func(trusted bool) *store.Thread {
		th := &store.Thread{PermissionMode: "plan", Permissions: map[string]string{"r1": "Bash", "p1": exitPlanMode}}
		th.OwnerID, th.Trusted = "ann", trusted
		return th
	}
	ans := func(id string, d bandav1.PermissionDecision, m bandav1.PermissionMode) *bandav1.PermissionAnswerRequest {
		return &bandav1.PermissionAnswerRequest{RequestId: id, Decision: d, Mode: m}
	}

	th := thr(false)
	if _, _, err := answerPermission(th, "bob", ans("r1", allow, 0)); !errors.Is(err, thread.ErrForbidden) {
		t.Errorf("a teammate answered in an untrusted thread: %v", err)
	}
	if _, _, err := answerPermission(th, "ann", ans("gone", allow, 0)); !errors.Is(err, errNoPermission) {
		t.Errorf("answered an unknown request: %v", err)
	}
	res, cmd, err := answerPermission(th, "ann", ans("r1", always, 0))
	if err != nil || res.Decision != always || !slices.Equal(th.AllowedTools, []string{"Bash"}) || !slices.Equal(cmd.AllowedTools, []string{"Bash"}) || th.Permissions["r1"] != "" {
		t.Errorf("allow always: %v %v %v %v", res, cmd, th.AllowedTools, err)
	}
	if _, _, err := answerPermission(th, "ann", ans("r1", allow, 0)); !errors.Is(err, errNoPermission) {
		t.Error("answered twice")
	}

	th = thr(true)
	if _, _, err := answerPermission(th, "bob", ans("r1", deny, 0)); err != nil {
		t.Errorf("a teammate in a trusted thread: %v", err)
	}
	if _, _, err := answerPermission(th, "bob", ans("p1", allow, edits)); !errors.Is(err, thread.ErrForbidden) {
		t.Error("a teammate approved a plan")
	}
	if _, _, err := answerPermission(th, "ann", ans("p1", allow, 0)); err == nil {
		t.Error("approved a plan without the mode to work in")
	}
	res, cmd, err = answerPermission(th, "ann", ans("p1", always, edits))
	if err != nil || th.PermissionMode != "accept_edits" || res.Mode != edits || cmd.Mode != edits || res.Decision != allow || len(th.AllowedTools) != 0 {
		t.Errorf("plan approval: %v %v %q %v", res, cmd, th.PermissionMode, err)
	}
}
