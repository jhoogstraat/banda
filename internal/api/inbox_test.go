package api

import (
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestInboxChanged(t *testing.T) {
	s := New(nil, nil)
	shown := &inboxWatcher{user: "alice", poke: make(chan struct{}, 1), ids: map[string]bool{"t1": true}}
	other := &inboxWatcher{user: "bob", poke: make(chan struct{}, 1), ids: map[string]bool{}}
	s.inbox[shown], s.inbox[other] = struct{}{}, struct{}{}
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
	}{
		{"delta", "t1", 0, false, false},
		{"stored event of a shown thread", "t1", 5, true, false},
		{"stored event of another thread", "t2", 5, false, false},
		{"new thread", "t3", 1, true, true},
	} {
		s.broadcast(c.id, []*bandav1.Event{{Seq: c.seq}})
		if woken(shown) != c.shown || woken(other) != c.other {
			t.Errorf("%s: wrong watchers woken", c.name)
		}
	}
	s.pokeInbox("bob")
	if woken(shown) || !woken(other) {
		t.Error("pokeInbox woke the wrong user")
	}
}
