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
}

// Effect is what the caller must do once the new state is persisted.
type Effect int

const (
	None      Effect = iota
	StartTurn        // deliver the message (or pending prompt) to the agent
	StopTurn         // interrupt the running turn
	Provision        // (re)create the environment
)

var (
	ErrForbidden = errors.New("only the thread owner can do this")
	ErrBusy      = errors.New("the agent is still working")
	ErrNotReady  = errors.New("the environment is not ready")
)

type Action interface{ action() }

// Send is a message posted to the thread by a user.
type Send struct {
	By            string
	MentionsAgent bool
	MentionsUsers bool
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

type EnvLost struct{}

func (Send) action()      {}
func (Interrupt) action() {}
func (SetMode) action()   {}
func (Retry) action()     {}
func (EnvReady) action()  {}
func (TurnEnded) action() {}
func (EnvLost) action()   {}

func Apply(s State, a Action) (State, Effect, error) {
	switch a := a.(type) {
	case Send:
		turn := a.MentionsAgent || (s.Mode == AgentMode && a.By == s.OwnerID && !a.MentionsUsers)
		if !turn {
			// Someone other than the owner talking, or anyone mentioning a
			// person, makes the thread a conversation until the owner ends it.
			s.Mode = ChatMode
			return s, None, nil
		}
		switch s.Status {
		case Working:
			return s, None, ErrBusy
		case Starting, Errored:
			return s, None, ErrNotReady
		}
		s.Status = Working
		return s, StartTurn, nil

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
		if s.Status != Starting && s.Status != Errored {
			return s, None, nil
		}
		if a.Pending {
			s.Status = Working
			return s, StartTurn, nil
		}
		s.Status = AwaitingInput
		return s, None, nil

	case TurnEnded:
		if s.Status == Working {
			s.Status = AwaitingInput
		}
		return s, None, nil

	case EnvLost:
		s.Status = Errored
		return s, None, nil
	}
	return s, None, errors.New("unknown action")
}
