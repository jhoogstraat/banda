package thread

import "testing"

func TestApply(t *testing.T) {
	idle := State{OwnerID: "ann", Status: AwaitingInput, Mode: AgentMode}
	chat := State{OwnerID: "ann", Status: AwaitingInput, Mode: ChatMode}
	with := func(s State, f func(*State)) State { f(&s); return s }
	working := func(s State, turns int) State { s.Status, s.Turns = Working, turns; return s }

	tests := []struct {
		name   string
		in     State
		action Action
		want   State
		effect Effect
		err    error
	}{
		{"owner prompts agent", idle, Send{By: "ann"},
			working(idle, 1), StartTurn, nil},
		{"other user switches to chat", idle, Send{By: "bob"},
			chat, None, nil},
		{"other user mentioning agent asks the owner, stays agent mode", idle, Send{By: "bob", MentionsAgent: true},
			with(idle, func(s *State) { s.Requests = 1 }), RequestTurn, nil},
		{"requests pile up, even while starting", with(idle, func(s *State) { s.Status, s.Requests = Starting, 1 }), Send{By: "bob", MentionsAgent: true},
			with(idle, func(s *State) { s.Status, s.Requests = Starting, 2 }), RequestTurn, nil},
		{"a request leaves a suspended thread asleep", with(chat, func(s *State) { s.Status = Suspended }), Send{By: "bob", MentionsAgent: true},
			with(chat, func(s *State) { s.Status, s.Requests = Suspended, 1 }), RequestTurn, nil},
		{"trusted thread: other user mentioning agent starts turn", with(idle, func(s *State) { s.Trusted = true }), Send{By: "bob", MentionsAgent: true},
			with(working(idle, 1), func(s *State) { s.Trusted = true }), StartTurn, nil},
		{"the owner's agents start turns without approval", idle, Send{By: "agent:t2", MentionsAgent: true, OwnersAgent: true},
			working(idle, 1), StartTurn, nil},
		{"owner approves a request", with(chat, func(s *State) { s.Requests = 2 }), Approve{By: "ann"},
			with(working(chat, 1), func(s *State) { s.Requests = 1 }), StartTurn, nil},
		{"approving while working queues the turn", with(working(idle, 1), func(s *State) { s.Requests = 1 }), Approve{By: "ann"},
			working(idle, 2), StartTurn, nil},
		{"approving resumes a suspended thread", with(idle, func(s *State) { s.Status, s.Requests = Suspended, 1 }), Approve{By: "ann"},
			with(idle, func(s *State) { s.Status = Starting }), Provision, nil},
		{"approving while starting waits, the request stays", with(idle, func(s *State) { s.Status, s.Requests = Starting, 1 }), Approve{By: "ann"},
			with(idle, func(s *State) { s.Status, s.Requests = Starting, 1 }), None, ErrNotReady},
		{"only the owner approves", with(idle, func(s *State) { s.Requests = 1 }), Approve{By: "bob"},
			with(idle, func(s *State) { s.Requests = 1 }), None, ErrForbidden},
		{"nothing to approve", idle, Approve{By: "ann"},
			idle, None, ErrNoRequest},
		{"owner declines a request", with(idle, func(s *State) { s.Requests = 1 }), Decline{By: "ann"},
			idle, None, nil},
		{"only the owner declines", with(idle, func(s *State) { s.Requests = 1 }), Decline{By: "bob"},
			with(idle, func(s *State) { s.Requests = 1 }), None, ErrForbidden},
		{"owner trusts teammates", idle, Trust{By: "ann", Trusted: true},
			with(idle, func(s *State) { s.Trusted = true }), None, nil},
		{"only the owner trusts", idle, Trust{By: "bob", Trusted: true},
			idle, None, ErrForbidden},
		{"owner mentioning a person switches to chat", idle, Send{By: "ann", MentionsUsers: true},
			chat, None, nil},
		{"owner's review without the agent is a note", idle, Send{By: "ann", Review: true},
			idle, None, nil},
		{"owner's review asking the agent starts turn", idle, Send{By: "ann", Review: true, MentionsAgent: true},
			working(idle, 1), StartTurn, nil},
		{"other user's review switches to chat", idle, Send{By: "bob", Review: true},
			chat, None, nil},
		{"other user's review asking the agent needs approval", idle, Send{By: "bob", Review: true, MentionsAgent: true},
			with(idle, func(s *State) { s.Requests = 1 }), RequestTurn, nil},
		{"owner in chat mode must mention agent", chat, Send{By: "ann"},
			chat, None, nil},
		{"owner in chat mode mentioning agent starts turn", chat, Send{By: "ann", MentionsAgent: true},
			working(chat, 1), StartTurn, nil},
		{"prompt while working is queued", working(idle, 1), Send{By: "ann"},
			working(idle, 2), StartTurn, nil},
		{"a queued turn keeps it working", working(idle, 2), TurnEnded{},
			working(idle, 1), None, nil},
		{"a turn with a prompt folded into it ends both", working(idle, 2), TurnEnded{Prompts: 2},
			idle, None, nil},
		{"chat while starting is fine", with(idle, func(s *State) { s.Status = Starting }), Send{By: "bob"},
			with(chat, func(s *State) { s.Status = Starting }), None, nil},
		{"prompt while starting", with(idle, func(s *State) { s.Status = Starting }), Send{By: "ann"},
			with(idle, func(s *State) { s.Status = Starting }), None, ErrNotReady},
		{"only owner ends chat mode", chat, SetMode{By: "bob", Mode: AgentMode},
			chat, None, ErrForbidden},
		{"owner ends chat mode", chat, SetMode{By: "ann", Mode: AgentMode},
			idle, None, nil},
		{"interrupt waits for turn end", working(idle, 1), Interrupt{By: "ann"},
			working(idle, 1), StopTurn, nil},
		{"turn ended", working(idle, 1), TurnEnded{},
			idle, None, nil},
		{"env ready with initial prompt", with(idle, func(s *State) { s.Status = Starting }), EnvReady{Pending: true},
			working(idle, 1), StartTurn, nil},
		{"env ready after reconnect", with(idle, func(s *State) { s.Status = Errored }), EnvReady{},
			idle, None, nil},
		{"env lost mid-turn", working(idle, 1), EnvLost{},
			with(idle, func(s *State) { s.Status = Errored }), None, nil},
		{"env lost", idle, EnvLost{},
			with(idle, func(s *State) { s.Status = Errored }), None, nil},
		{"setup fails while starting", with(idle, func(s *State) { s.Status = Starting }), EnvLost{},
			with(idle, func(s *State) { s.Status = Errored }), None, nil},
		{"no prompt after failed setup", with(idle, func(s *State) { s.Status = Errored }), Send{By: "ann"},
			with(idle, func(s *State) { s.Status = Errored }), None, ErrNotReady},
		{"retry", with(idle, func(s *State) { s.Status = Errored }), Retry{By: "ann"},
			with(idle, func(s *State) { s.Status = Starting }), Provision, nil},
		{"idle thread suspends", idle, Suspend{},
			with(idle, func(s *State) { s.Status = Suspended }), SuspendEnv, nil},
		{"working thread does not suspend", working(idle, 1), Suspend{},
			working(idle, 1), None, nil},
		{"prompt resumes a suspended thread", with(idle, func(s *State) { s.Status = Suspended }), Send{By: "ann"},
			with(idle, func(s *State) { s.Status = Starting }), Provision, nil},
		{"chat in a suspended thread keeps it suspended", with(idle, func(s *State) { s.Status = Suspended }), Send{By: "bob"},
			with(chat, func(s *State) { s.Status = Suspended }), None, nil},
		{"runner stopping during suspend is expected", with(idle, func(s *State) { s.Status = Suspended }), EnvLost{},
			with(idle, func(s *State) { s.Status = Suspended }), None, nil},
		{"runner restarted mid-turn", working(idle, 2), EnvReady{},
			idle, None, nil},
		{"dying pod reporting ready is ignored", with(idle, func(s *State) { s.Status = Suspended }), EnvReady{},
			with(idle, func(s *State) { s.Status = Suspended }), None, nil},
		{"owner wakes a suspended thread", with(idle, func(s *State) { s.Status = Suspended }), Wake{By: "ann"},
			with(idle, func(s *State) { s.Status = Starting }), Provision, nil},
		{"others cannot wake it", with(idle, func(s *State) { s.Status = Suspended }), Wake{By: "bob"},
			with(idle, func(s *State) { s.Status = Suspended }), None, ErrForbidden},
	}
	for _, tt := range tests {
		got, effect, err := Apply(tt.in, tt.action)
		if got != tt.want || effect != tt.effect || err != tt.err {
			t.Errorf("%s: got %+v, %v, %v; want %+v, %v, %v", tt.name, got, effect, err, tt.want, tt.effect, tt.err)
		}
	}
}
