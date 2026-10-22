// Package api implements the ThreadService, UserService and SettingsService
// for the web client and the RunnerService the sandbox runners dial into.
package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/sandbox"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

type Service struct {
	store     *store.Store
	sandboxes *sandbox.Client
	// DeleteSettledAfter is how long after the owner settles a thread its
	// pushed environment is kept.
	DeleteSettledAfter time.Duration
	// LoginIdle is how long a login environment runs without a terminal.
	LoginIdle time.Duration
	// MaxSubthreadDepth is how deep agents may nest sub-threads, and
	// MaxAgentThreads how many sub-threads started by agents may run per owner.
	MaxSubthreadDepth, MaxAgentThreads int
	// OverlapWindow is how recently two threads must have changed for their
	// shared files to count as an overlap; zero turns overlaps off.
	OverlapWindow time.Duration
	// SuspendPause holds every suspend between the thread's status and its
	// environment, so tests can race it. Zero outside tests.
	SuspendPause time.Duration
	// WatchBuffer is how many events a WatchThread stream may lag behind
	// before it catches up from the store.
	WatchBuffer int

	mu sync.Mutex
	// watchers holds each WatchThread's channel and its behind signal (see watch).
	watchers map[string]map[chan *bandav1.Event]chan struct{}
	// present counts each thread's open WatchThread streams per user.
	present map[string]map[string]int
	// runners and terminals are keyed by thread id, or loginKey for login
	// environments.
	runners map[string]*runner
	// terminals holds each runner's single terminal subscriber (see terminal).
	terminals map[string]chan *bandav1.TerminalOutput
	// loginLastUsed is when each user last had a login terminal.
	loginLastUsed map[string]time.Time
	// files holds the requests waiting for a runner's answer (see fileRequest).
	files map[string]chan *bandav1.FileResponse
	// pushes holds the suspends waiting for their runner's push (see pushAll).
	pushes map[string]chan *bandav1.AutoPush
	// parking holds the threads to suspend once their turn ends, and how
	// their owner put them away: "snoozed" or "settled" (see park).
	// ponytail: in memory, an API restart forgets them; the idle sweep still suspends them.
	parking map[string]string
	// previews is closed and removed when a thread's preview changes: its
	// runner came or went, or reported other ports (see PreviewWatch).
	previews map[string]chan struct{}
	inbox    map[*inboxWatcher]struct{}
	// reports holds the latest lineage report of each thread until it is done.
	reports map[string]chan struct{}
	// deliveries holds a mutex per thread, see lockDelivery.
	deliveries sync.Map

	// Preview configures the preview origin (preview.go). Set before serving.
	Preview      PreviewConfig
	previewKey   signer
	previewProxy *http.Transport
}

func New(st *store.Store, sb *sandbox.Client) *Service {
	s := &Service{
		store:     st,
		sandboxes: sb,
		watchers:  map[string]map[chan *bandav1.Event]chan struct{}{},
		present:   map[string]map[string]int{},
		runners:   map[string]*runner{},
		terminals: map[string]chan *bandav1.TerminalOutput{},
		files:     map[string]chan *bandav1.FileResponse{},
		pushes:    map[string]chan *bandav1.AutoPush{},
		parking:   map[string]string{},
		previews:  map[string]chan struct{}{},
		inbox:     map[*inboxWatcher]struct{}{},
		reports:   map[string]chan struct{}{},

		DeleteSettledAfter: 7 * 24 * time.Hour,
		LoginIdle:          10 * time.Minute,
		MaxSubthreadDepth:  3,
		MaxAgentThreads:    8,
		OverlapWindow:      7 * 24 * time.Hour,
		WatchBuffer:        256,
		loginLastUsed:      map[string]time.Time{},
		previewKey:         signer(rand.Text()),
	}
	s.previewProxy = &http.Transport{DialContext: s.dialPreview, MaxIdleConnsPerHost: 8, IdleConnTimeout: time.Minute, DisableCompression: true}
	return s
}

// apply runs an action (nil = none) through the state machine and stores the
// given events plus any status/mode change, then fans them out.
func (s *Service) apply(ctx context.Context, id string, a thread.Action, events ...*bandav1.Event) (thread.Effect, error) {
	return s.applyBecause(ctx, id, a, "", events...)
}

// applyBecause is apply with a reason for the status change.
func (s *Service) applyBecause(ctx context.Context, id string, a thread.Action, reason string, events ...*bandav1.Event) (thread.Effect, error) {
	var effect thread.Effect
	_, stored, err := s.store.Update(ctx, id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		if a == nil {
			return events, nil
		}
		next, eff, err := thread.Apply(t.State, a)
		if err != nil {
			return nil, err
		}
		effect = eff
		changes := stateEvents(t.State, next)
		for _, c := range changes {
			if sc := c.GetStatusChanged(); sc != nil {
				sc.Reason = reason
			}
		}
		out := append(events, changes...)
		t.State = next
		return out, nil
	})
	if err != nil {
		return thread.None, err
	}
	s.broadcast(id, stored)
	return effect, nil
}

// do carries out an effect. StartTurn needs a prompt, so callers handle it.
func (s *Service) do(ctx context.Context, id string, e thread.Effect) {
	switch e {
	case thread.StopTurn:
		s.sendRunner(id, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Stop{Stop: &bandav1.Stop{}}})
	case thread.Provision:
		if t, err := s.store.Get(ctx, id); err == nil {
			s.provision(ctx, t)
		}
	case thread.SuspendEnv:
		ctx = context.WithoutCancel(ctx) // a caller going away must not leave it half done
		pushed := s.pushAll(id)
		time.Sleep(s.SuspendPause)
		// Under the thread's lock: a post or wake since Suspend wins, and its
		// environment stays up.
		_, _, err := s.store.Update(ctx, id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
			if t.Status != thread.Suspended {
				return nil, errResumed
			}
			t.EnvPushed = pushed
			return nil, s.sandboxes.Suspend(ctx, id)
		})
		if err == nil {
			s.scheduleDelete(ctx, id)
		} else if !errors.Is(err, errResumed) {
			slog.Error("suspend sandbox", "thread", id, "err", err)
		}
	}
}

var errResumed = errors.New("resumed while suspending")

// pushAll has the thread's runner push once more, after any push still
// running, and reports whether all its work is on origin. Without a runner,
// or an answer within a minute, nobody knows.
func (s *Service) pushAll(id string) bool {
	ch := make(chan *bandav1.AutoPush, 1)
	s.mu.Lock()
	r := s.runners[id]
	s.pushes[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.pushes[id] == ch {
			delete(s.pushes, id)
		}
		s.mu.Unlock()
	}()
	if r == nil || !r.send(&bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Push{Push: &bandav1.Push{Suspend: true}}}) {
		return false
	}
	select {
	case p := <-ch:
		return p.State == bandav1.AutoPushState_AUTO_PUSH_STATE_PUSHED
	case <-r.done: // e.g. a post restarted the environment
	case <-time.After(time.Minute):
	}
	return false
}

func messageEvent(by string, m *bandav1.Message) *bandav1.Event {
	return &bandav1.Event{Actor: by, Kind: &bandav1.Event_Message{Message: m}}
}

func stateEvents(old, next thread.State) []*bandav1.Event {
	var out []*bandav1.Event
	if old.Status != next.Status {
		out = append(out, &bandav1.Event{Actor: "system", Kind: &bandav1.Event_StatusChanged{
			StatusChanged: &bandav1.StatusChanged{Status: statusToProto[next.Status]}}})
	}
	if old.Mode != next.Mode {
		out = append(out, &bandav1.Event{Actor: "system", Kind: &bandav1.Event_ModeChanged{
			ModeChanged: &bandav1.ModeChanged{Mode: modeToProto[next.Mode]}}})
	}
	return out
}

var (
	statusToProto = map[thread.Status]bandav1.Status{
		thread.Starting:      bandav1.Status_STATUS_STARTING,
		thread.Working:       bandav1.Status_STATUS_WORKING,
		thread.AwaitingInput: bandav1.Status_STATUS_AWAITING_INPUT,
		thread.Errored:       bandav1.Status_STATUS_ERRORED,
		thread.Suspended:     bandav1.Status_STATUS_SUSPENDED,
	}
	modeToProto   = map[thread.Mode]bandav1.Mode{thread.AgentMode: bandav1.Mode_MODE_AGENT, thread.ChatMode: bandav1.Mode_MODE_CHAT}
	modeFromProto = map[bandav1.Mode]thread.Mode{bandav1.Mode_MODE_AGENT: thread.AgentMode, bandav1.Mode_MODE_CHAT: thread.ChatMode}
)

func toProto(t store.Thread) *bandav1.Thread {
	p := &bandav1.Thread{
		Id:          t.ID,
		OwnerId:     t.OwnerID,
		Title:       t.Title,
		RepoUrl:     t.RepoURL,
		Branch:      t.Branch,
		Model:       t.Model,
		Effort:      t.Effort,
		Status:      statusToProto[t.Status],
		Mode:        modeToProto[t.Mode],
		Diff:        t.Diff,
		CreatedAt:   timestamppb.New(t.CreatedAt),
		UpdatedAt:   timestamppb.New(t.UpdatedAt),
		BaseBranch:  t.BaseBranch,
		Settled:     t.Settled,
		Unread:      t.Unread,
		ParentId:    t.ParentID,
		ParentTitle: t.ParentTitle,

		TurnRequests:       int32(t.Requests),
		Trusted:            t.Trusted,
		AutoPush:           bandav1.AutoPushState(bandav1.AutoPushState_value[t.AutoPush]),
		EnvironmentDeleted: t.EnvDeleteAt != nil && !t.EnvDeleteAt.After(time.Now()),
		Activity:           t.Activity,
		PermissionMode:     permissionModeToProto[t.PermissionMode],
		PermissionPending:  len(t.Permissions) > 0,
		PermissionTools:    slices.Sorted(maps.Values(t.Permissions)),
		RaceId:             t.RaceID,
		PullRequestUrl:     pullRequestURL(t),
	}
	if t.ForkedFrom != "" {
		p.ForkedFrom = &bandav1.ForkOrigin{ThreadId: t.ForkedFrom, Turn: int32(t.ForkTurn)}
	}
	if t.SnoozedUntil != nil {
		p.SnoozedUntil = timestamppb.New(*t.SnoozedUntil)
	}
	return p
}

var githubRepo = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:)([^/]+/[^/]+?)(?:\.git)?/?$`)

// pullRequestURL opens a GitHub pull request for the thread's branch, once
// all its work is pushed.
func pullRequestURL(t store.Thread) string {
	m := githubRepo.FindStringSubmatch(t.RepoURL)
	if m == nil || t.AutoPush != bandav1.AutoPushState_AUTO_PUSH_STATE_PUSHED.String() {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/compare/%s...%s?expand=1", m[1], t.BaseBranch, t.Branch)
}

// title is a thread's title from its first prompt.
func title(prompt string) string { return firstLine(prompt, 60) }

// firstLine is the first line of s, cut to n runes.
func firstLine(s string, n int) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(line); len(r) > n {
		return string(r[:n]) + "…"
	}
	return line
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
