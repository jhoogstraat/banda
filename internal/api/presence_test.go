package api

import (
	"slices"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestPresence(t *testing.T) {
	s := New(nil, nil)
	got := func(ch chan *bandav1.Event) []string {
		t.Helper()
		e := <-ch
		return e.GetPresenceChanged().GetUserIds()
	}
	alice, _ := s.watch("t", "alice")
	if u := got(alice); !slices.Equal(u, []string{"alice"}) {
		t.Fatalf("alice joins: %v", u)
	}
	bob1, _ := s.watch("t", "bob")
	bob2, _ := s.watch("t", "bob") // a second tab
	for _, want := range [][]string{{"alice", "bob"}, {"alice", "bob"}} {
		if u := got(alice); !slices.Equal(u, want) {
			t.Fatalf("bob joins: %v", u)
		}
	}
	if u := got(bob2); !slices.Equal(u, []string{"alice", "bob"}) {
		t.Fatalf("a new watcher gets the presence: %v", u)
	}
	s.unwatch("t", "bob", bob1)
	select {
	case e := <-alice:
		t.Fatalf("bob is still here in another tab, got %v", e)
	default:
	}
	s.unwatch("t", "bob", bob2)
	if u := got(alice); !slices.Equal(u, []string{"alice"}) {
		t.Fatalf("bob leaves: %v", u)
	}
	s.unwatch("t", "alice", alice)
	if len(s.present) != 0 {
		t.Fatalf("presence left behind: %v", s.present)
	}
}
