// Package thread is the thread state machine. Apply is pure: callers persist
// the new state and then carry out the returned Effect.
package thread

import "errors"

type Status string

const (
	Starting      Status = "starting"
	Working       Status = "working"
	AwaitingInput Status = "awaiting_input"
	Errored       Status = "errored"
	Suspended     Status = "suspended" // idle, environment paused
)

type Mode string

const (
	AgentMode Mode = "agent" // every owner message starts a turn
	ChatMode  Mode = "chat"  // only messages mentioning the agent start a turn
)

type State struct {
	OwnerID string
	Status  Status
	Mode    Mode
	// Turns delivered to the agent and not yet ended. Claude Code queues
	// prompts sent mid-turn, so a thread stays working until all have ended.
	Turns int
	// Requests is the number of teammate turn requests waiting for the
	// owner. Unless Trusted, a teammate's turn needs the owner's approval: it
	// runs on the owner's subscription, in a pod holding their credentials.
	Requests int
	Trusted  bool
}

// Effect is what the caller must do once the new state is persisted.
type Effect int

const (
	None        Effect = iota
	StartTurn          // deliver the message (or pending prompt) to the agent
	RequestTurn        // keep the message as a request for the owner
	StopTurn           // interrupt the running turn
	Provision          // (re)create or resume the environment
	SuspendEnv         // pause the environment, keeping its volumes
)

var (
	ErrForbidden = errors.New("only the thread owner can do this")
	ErrNotReady  = errors.New("the environment is not ready")
	ErrNoRequest = errors.New("no turn request is waiting")
)

type Action interface{ action() }

// Send is a message posted to the thread by a user.
type Send struct {
	By            string
	MentionsAgent bool
	MentionsUsers bool
	// Review: a diff review. The owner's review starts a turn only when it
	// mentions the agent, and otherwise leaves the mode alone.
	Review bool
	// OwnersAgent: an agent of the owner's other threads, posting through MCP.
	OwnersAgent bool
}

// Approve runs a teammate's turn request as their turn; Decline drops it.
type Approve struct{ By string }

type Decline struct{ By string }

// Trust lets teammates start turns without approval.
type Trust struct {
	By      string
	Trusted bool
}

type Interrupt struct{ By string }

type SetMode struct {
	By   string
	Mode Mode
}

type Retry struct{ By string }

// EnvReady: the runner connected. Pending is true if a prompt is waiting
// to be delivered (the initial prompt of a new thread).
type EnvReady struct{ Pending bool }

type TurnEnded struct{}

// EnvLost: the runner disconnected, or a setup step failed (the runner then
// stays connected so the owner can debug in the terminal; Retry reruns setup).
type EnvLost struct{}

// Suspend: the thread has been idle for a while, or its owner snoozed or
// settled it.
type Suspend struct{}

// Wake resumes a suspended environment without a prompt, e.g. to open a terminal.
type Wake struct{ By string }

func (Send) action()      {}
func (Approve) action()   {}
func (Decline) action()   {}
func (Trust) action()     {}
func (Interrupt) action() {}
func (SetMode) action()   {}
func (Retry) action()     {}
func (EnvReady) action()  {}
func (TurnEnded) action() {}
func (EnvLost) action()   {}
func (Suspend) action()   {}
func (Wake) action()      {}

func Apply(s State, a Action) (State, Effect, error) {
	switch a := a.(type) {
	case Send:
		if a.Review && !a.MentionsAgent && a.By == s.OwnerID && !a.MentionsUsers {
			return s, None, nil
		}
		turn := a.MentionsAgent || (s.Mode == AgentMode && a.By == s.OwnerID && !a.MentionsUsers)
		if !turn {
			// Someone other than the owner talking, or anyone mentioning a
			// person, makes the thread a conversation until the owner ends it.
			s.Mode = ChatMode
			return s, None, nil
		}
		if a.By != s.OwnerID && !a.OwnersAgent && !s.Trusted {
			s.Requests++
			return s, RequestTurn, nil
		}
		return startTurn(s)

	case Approve:
		if a.By != s.OwnerID {
			return s, None, ErrForbidden
		}
		if s.Requests == 0 {
			return s, None, ErrNoRequest
		}
		next, eff, err := startTurn(s)
		if err == nil {
			next.Requests--
		}
		return next, eff, err

	case Decline:
		if a.By != s.OwnerID {
			return s, None, ErrForbidden
		}
		if s.Requests == 0 {
			return s, None, ErrNoRequest
		}
		s.Requests--
		return s, None, nil

	case Trust:
		if a.By != s.OwnerID {
			return s, None, ErrForbidden
		}
		s.Trusted = a.Trusted
		return s, None, nil

	case Interrupt:
		if a.By != s.OwnerID {
			return s, None, ErrForbidden
		}
		if s.Status != Working {
			return s, None, nil
		}
		// Status changes when the agent reports the turn as ended.
		return s, StopTurn, nil

	case SetMode:
		if a.By != s.OwnerID {
			return s, None, ErrForbidden
		}
		s.Mode = a.Mode
		return s, None, nil

	case Retry:
		if a.By != s.OwnerID {
			return s, None, ErrForbidden
		}
		if s.Status != Errored {
			return s, None, nil
		}
		s.Status = Starting
		return s, Provision, nil

	case EnvReady:
		// A fresh claude runs no turn, so this also ends a turn lost with a
		// restarted API. Ignored while suspended: that is a dying pod;
		// resuming goes through Starting.
		if s.Status == AwaitingInput || s.Status == Suspended {
			return s, None, nil
		}
		if a.Pending {
			s.Status, s.Turns = Working, 1
			return s, StartTurn, nil
		}
		s.Status, s.Turns = AwaitingInput, 0
		return s, None, nil

	case TurnEnded:
		if s.Status == Working {
			s.Turns = max(s.Turns-1, 0)
			if s.Turns == 0 {
				s.Status = AwaitingInput
			}
		}
		return s, None, nil

	case EnvLost:
		if s.Status != Suspended { // suspending stops the runner on purpose
			s.Status, s.Turns = Errored, 0
		}
		return s, None, nil

	case Suspend:
		if s.Status == Working || s.Status == Suspended {
			return s, None, nil
		}
		s.Status = Suspended
		return s, SuspendEnv, nil

	case Wake:
		if a.By != s.OwnerID {
			return s, None, ErrForbidden
		}
		if s.Status != Suspended {
			return s, None, nil
		}
		s.Status = Starting
		return s, Provision, nil
	}
	return s, None, errors.New("unknown action")
}

func startTurn(s State) (State, Effect, error) {
	switch s.Status {
	case Starting, Errored:
		return s, None, ErrNotReady
	case Suspended:
		// The caller keeps the message as the pending prompt.
		s.Status = Starting
		return s, Provision, nil
	}
	s.Status, s.Turns = Working, s.Turns+1
	return s, StartTurn, nil
}
