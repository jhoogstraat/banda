package api

import (
	"fmt"
	"strings"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// agentPrompt hands the agent the thread messages it hasn't seen yet (chat
// between people, posts by other agents) and says who is asking when it
// isn't the owner. A plain owner prompt is passed through unchanged.
func agentPrompt(owner string, names map[string]string, unseen []*bandav1.Event, by, text string) string {
	name := func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return id
	}
	var b strings.Builder
	for _, e := range unseen {
		if m := e.GetMessage(); m != nil {
			if b.Len() == 0 {
				b.WriteString("Messages posted in this thread since your last turn:\n")
			}
			fmt.Fprintf(&b, "%s: %s\n", name(e.Actor), m.Text)
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
