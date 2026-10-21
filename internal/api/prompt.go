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
func agentPrompt(owner string, names map[string]string, unseen []*bandav1.Event, by string, m *bandav1.Message) string {
	text := messageText(m)
	name := func(id string) string { return actorName(names, id) }
	var b strings.Builder
	for _, e := range unseen {
		if m := e.GetMessage(); m != nil {
			if b.Len() == 0 {
				b.WriteString("Messages posted in this thread since your last turn:\n")
			}
			fmt.Fprintf(&b, "%s: %s\n", name(e.Actor), messageText(m))
		}
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

// messageText is a message as plain text, its review comments rendered as
// markdown: each comment's file and lines, the quoted diff lines, the comment.
func messageText(m *bandav1.Message) string {
	if len(m.Review) == 0 {
		return m.Text
	}
	var b strings.Builder
	b.WriteString(m.Text)
	if m.Text != "" {
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Review of the diff (%d comment", len(m.Review))
	if len(m.Review) > 1 {
		b.WriteString("s")
	}
	b.WriteString("):")
	for _, c := range m.Review {
		lines := fmt.Sprintf("line %d", c.EndLine)
		if c.StartLine != c.EndLine {
			lines = fmt.Sprintf("lines %d-%d", c.StartLine, c.EndLine)
		}
		if c.Old {
			lines += " (removed, numbered as in the base branch)"
		}
		// A fence longer than any backtick run in the code.
		fence := "```"
		for strings.Contains(c.Code, fence) {
			fence += "`"
		}
		fmt.Fprintf(&b, "\n\n%s, %s:\n%sdiff\n%s\n%s\n%s", c.Path, lines, fence, strings.TrimRight(c.Code, "\n"), fence, c.Body)
	}
	return b.String()
}
