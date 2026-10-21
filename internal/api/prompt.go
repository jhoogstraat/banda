package api

import (
	"fmt"
	"strings"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// actorName is the display name of an event actor: a user's name, or for
// "agent:<thread>" (an agent posting from another thread) a description.
func actorName(names map[string]string, id string) string {
	if n := names[id]; n != "" {
		return n
	}
	if tid, ok := strings.CutPrefix(id, "agent:"); ok {
		return "agent in thread " + tid
	}
	return id
}

// agentPrompt hands the agent the thread messages it hasn't seen yet (chat
// between people, posts by other agents) and says who is asking when it
// isn't the owner. A plain owner prompt is passed through unchanged.
func agentPrompt(owner string, names map[string]string, unseen []*bandav1.Event, by, text string) string {
	name := func(id string) string { return actorName(names, id) }
	var b strings.Builder
	for _, e := range unseen {
		line := ""
		if m := e.GetMessage(); m != nil {
			line = name(e.Actor) + ": " + m.Text
		} else if u := e.GetLineageUpdate(); u != nil {
			line = lineageLine(u)
		}
		if line == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("Messages posted in this thread since your last turn:\n")
		}
		b.WriteString(line + "\n")
	}
	if b.Len() == 0 && by == owner {
		return text
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%s: %s", name(by), text)
	return b.String()
}

// lineageLine describes a sub-thread update for the agent and transcripts.
func lineageLine(u *bandav1.LineageUpdate) string {
	what := map[bandav1.Status]string{bandav1.Status_STATUS_STARTING: "was spawned",
		bandav1.Status_STATUS_AWAITING_INPUT: "finished", bandav1.Status_STATUS_ERRORED: "failed"}[u.Status]
	line := fmt.Sprintf("[Sub-thread %s %q %s]", u.ThreadId, u.Title, what)
	if u.Text != "" {
		line += " " + u.Text
	}
	return line
}
