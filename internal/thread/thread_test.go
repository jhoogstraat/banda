package thread

import "testing"

func TestApply(t *testing.T) {
	idle := State{OwnerID: "ann", Status: AwaitingInput, Mode: AgentMode}
	chat := State{OwnerID: "ann", Status: AwaitingInput, Mode: ChatMode}
	with := func(s State, f func(*State)) State { f(&s); return s }

	tests := []struct {
		name   string
		in     State
		action Action
		want   State
		effect Effect
		err    error
	}{
		{"owner prompts agent", idle, Send{By: "ann"},
			with(idle, func(s *State) { s.Status = Working }), StartTurn, nil},
		{"other user switches to chat", idle, Send{By: "bob"},
			chat, None, nil},
		{"other user mentioning agent starts turn, stays agent mode", idle, Send{By: "bob", MentionsAgent: true},
			with(idle, func(s *State) { s.Status = Working }), StartTurn, nil},
		{"owner mentioning a person switches to chat", idle, Send{By: "ann", MentionsUsers: true},
			chat, None, nil},
		{"owner in chat mode must mention agent", chat, Send{By: "ann"},
			chat, None, nil},
		{"owner in chat mode mentioning agent starts turn", chat, Send{By: "ann", MentionsAgent: true},
			with(chat, func(s *State) { s.Status = Working }), StartTurn, nil},
		{"busy", with(idle, func(s *State) { s.Status = Working }), Send{By: "ann"},
			with(idle, func(s *State) { s.Status = Working }), None, ErrBusy},
		{"chat while starting is fine", with(idle, func(s *State) { s.Status = Starting }), Send{By: "bob"},
			with(chat, func(s *State) { s.Status = Starting }), None, nil},
		{"prompt while starting", with(idle, func(s *State) { s.Status = Starting }), Send{By: "ann"},
			with(idle, func(s *State) { s.Status = Starting }), None, ErrNotReady},
		{"only owner ends chat mode", chat, SetMode{By: "bob", Mode: AgentMode},
			chat, None, ErrForbidden},
		{"owner ends chat mode", chat, SetMode{By: "ann", Mode: AgentMode},
			idle, None, nil},
		{"interrupt waits for turn end", with(idle, func(s *State) { s.Status = Working }), Interrupt{By: "ann"},
			with(idle, func(s *State) { s.Status = Working }), StopTurn, nil},
		{"turn ended", with(idle, func(s *State) { s.Status = Working }), TurnEnded{},
			idle, None, nil},
		{"env ready with initial prompt", with(idle, func(s *State) { s.Status = Starting }), EnvReady{Pending: true},
			with(idle, func(s *State) { s.Status = Working }), StartTurn, nil},
		{"env ready after reconnect", with(idle, func(s *State) { s.Status = Errored }), EnvReady{},
			idle, None, nil},
		{"env lost", idle, EnvLost{},
			with(idle, func(s *State) { s.Status = Errored }), None, nil},
		{"retry", with(idle, func(s *State) { s.Status = Errored }), Retry{By: "ann"},
			with(idle, func(s *State) { s.Status = Starting }), Provision, nil},
		{"idle thread suspends", idle, Suspend{},
			with(idle, func(s *State) { s.Status = Suspended }), SuspendEnv, nil},
		{"working thread does not suspend", with(idle, func(s *State) { s.Status = Working }), Suspend{},
			with(idle, func(s *State) { s.Status = Working }), None, nil},
		{"prompt resumes a suspended thread", with(idle, func(s *State) { s.Status = Suspended }), Send{By: "ann"},
			with(idle, func(s *State) { s.Status = Starting }), Provision, nil},
		{"chat in a suspended thread keeps it suspended", with(idle, func(s *State) { s.Status = Suspended }), Send{By: "bob"},
			with(chat, func(s *State) { s.Status = Suspended }), None, nil},
		{"runner stopping during suspend is expected", with(idle, func(s *State) { s.Status = Suspended }), EnvLost{},
			with(idle, func(s *State) { s.Status = Suspended }), None, nil},
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
