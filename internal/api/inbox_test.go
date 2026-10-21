package api

import (
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestInboxChanged(t *testing.T) {
	s := New(nil, nil)
	shown := &inboxWatcher{user: "alice", poke: make(chan struct{}, 1), ids: map[string]bool{"t1": true}}
	other := &inboxWatcher{user: "bob", poke: make(chan struct{}, 1), ids: map[string]bool{}}
	team := &inboxWatcher{user: "carol", poke: make(chan struct{}, 1), team: true}
	s.inbox[shown], s.inbox[other], s.inbox[team] = struct{}{}, struct{}{}, struct{}{}
	woken := func(w *inboxWatcher) bool {
		select {
		case <-w.poke:
			return true
		default:
			return false
		}
	}
	for _, c := range []struct {
		name         string
		id           string
		seq          int64
		shown, other bool
		team         bool
	}{
		{"delta", "t1", 0, false, false, false},
		{"stored event of a shown thread", "t1", 5, true, false, true},
		{"stored event of another thread", "t2", 5, false, false, true},
		{"new thread", "t3", 1, true, true, true},
	} {
		s.broadcast(c.id, []*bandav1.Event{{Seq: c.seq}})
		if woken(shown) != c.shown || woken(other) != c.other || woken(team) != c.team {
			t.Errorf("%s: wrong watchers woken", c.name)
		}
	}
	s.pokeInbox("bob")
	if woken(shown) || !woken(other) || woken(team) {
		t.Error("pokeInbox woke the wrong user")
	}
	ch := s.watch("t1", "dave")
	if woken(shown) || woken(other) || !woken(team) {
		t.Error("presence should wake only team watchers")
	}
	s.unwatch("t1", "dave", ch)
	if !woken(team) {
		t.Error("leaving should wake team watchers")
	}
}
