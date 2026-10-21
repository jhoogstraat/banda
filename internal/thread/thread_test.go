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
		{"other user mentioning agent starts turn, stays agent mode", idle, Send{By: "bob", MentionsAgent: true},
			working(idle, 1), StartTurn, nil},
		{"owner mentioning a person switches to chat", idle, Send{By: "ann", MentionsUsers: true},
			chat, None, nil},
		{"owner's review without the agent is a note", idle, Send{By: "ann", Review: true},
			idle, None, nil},
		{"owner's review asking the agent starts turn", idle, Send{By: "ann", Review: true, MentionsAgent: true},
			working(idle, 1), StartTurn, nil},
		{"other user's review switches to chat", idle, Send{By: "bob", Review: true},
			chat, None, nil},
		{"owner in chat mode must mention agent", chat, Send{By: "ann"},
			chat, None, nil},
		{"owner in chat mode mentioning agent starts turn", chat, Send{By: "ann", MentionsAgent: true},
			working(chat, 1), StartTurn, nil},
		{"prompt while working is queued", working(idle, 1), Send{By: "ann"},
			working(idle, 2), StartTurn, nil},
		{"a queued turn keeps it working", working(idle, 2), TurnEnded{},
			working(idle, 1), None, nil},
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
