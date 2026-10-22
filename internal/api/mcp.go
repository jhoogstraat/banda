package api

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
)

type callerKey struct{}

var mcpSchemas = mcp.NewSchemaCache()

const maxListed, maxDiff = 50, 20 << 10

// MCPHandler serves the Banda MCP server, which lets an agent see and talk to
// other threads. A request authenticates as the calling thread with its runner
// token (Authorization: Bearer ...) and its id (X-Banda-Thread); the thread's
// owner is the identity the tools act for.
// ponytail: stateless, one MCP server per request; add sessions only for server-to-client features.
func (s *Service) MCPHandler() http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s.mcpServer(r.Context().Value(callerKey{}).(store.Thread))
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
		// Runners reach a host-run API through the container VM's loopback with a
		// non-local Host header. Every request needs the thread's bearer token,
		// which a DNS-rebinding page cannot have.
		DisableLocalhostProtection: true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		t, err := s.runnerThread(r.Context(), r.Header.Get("X-Banda-Thread"), token)
		if connect.CodeOf(err) == connect.CodeUnauthenticated {
			http.Error(w, "unknown thread or bad token", http.StatusUnauthorized)
			return
		} else if err != nil {
			slog.Error("mcp auth", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		ctx := WithUser(context.WithValue(r.Context(), callerKey{}, t), "agent:"+t.ID) // for logs; tools act as t.OwnerID
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

type threadInfo struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	OwnerID   string    `json:"owner_id"`
	OwnerName string    `json:"owner_name"`
	Status    string    `json:"status"`
	Mode      string    `json:"mode"`
	Repo      string    `json:"repo"`
	Branch    string    `json:"branch"`
	UpdatedAt time.Time `json:"updated_at"`
	ParentID  string    `json:"parent_id,omitempty"`
}

type (
	listIn struct {
		Owner string `json:"owner,omitempty" jsonschema:"'me' for threads owned by the same person as this thread, 'all' (default) for everyone's, or a user id"`
	}
	listOut struct {
		Threads []threadInfo `json:"threads"`
	}
	readIn struct {
		ThreadID    string `json:"thread_id,omitempty" jsonschema:"id of the thread to read (default: this thread)"`
		Limit       int    `json:"limit,omitempty" jsonschema:"how many of the most recent transcript lines to return (default 50)"`
		IncludeDiff bool   `json:"include_diff,omitempty" jsonschema:"also return the thread's git diff, truncated to 20 KB"`
	}
	postIn struct {
		ThreadID     string `json:"thread_id" jsonschema:"id of the thread to post into"`
		Text         string `json:"text" jsonschema:"the message"`
		MentionAgent bool   `json:"mention_agent,omitempty" jsonschema:"make that thread's agent act on the message; only allowed for threads owned by the same person as this thread"`
	}
	childrenIn struct {
		ThreadID string `json:"thread_id,omitempty" jsonschema:"thread whose sub-threads to list (default: this thread)"`
	}
	createIn struct {
		Prompt     string `json:"prompt" jsonschema:"the task for the new thread's agent"`
		RepoURL    string `json:"repo_url,omitempty" jsonschema:"git repository to clone (default: this thread's, none if it has none)"`
		BaseBranch string `json:"base_branch,omitempty" jsonschema:"branch to start from (default: this thread's branch if it was pushed, else this thread's base branch)"`
		Model      string `json:"model,omitempty" jsonschema:"model (default: this thread's)"`
		Effort     string `json:"effort,omitempty" jsonschema:"effort (default: this thread's)"`
		Profile    string `json:"profile,omitempty" jsonschema:"who the new agent works with: engineer, spec, devops or business (default: this thread's)"`
		WakeParent bool   `json:"wake_parent,omitempty" jsonschema:"when the sub-thread finishes its turns or fails, also start a turn of yours to act on it (default false)"`
	}
	handoffIn struct {
		To       string `json:"to" jsonschema:"the teammate to hand this thread to: their user id or name (see owner_id and owner_name in list_threads)"`
		Briefing string `json:"briefing" jsonschema:"for them: the state of the work, what's done, what's next and the open risks"`
	}
	createOut struct {
		ThreadID string `json:"thread_id"`
	}
	searchIn struct {
		Query string `json:"query" jsonschema:"words to find, all of which must match; each also matches as a prefix"`
		Kind  string `json:"kind,omitempty" jsonschema:"only 'people' (messages), 'agent' (agent text) or 'code' (tool calls and review comments)"`
		Limit int    `json:"limit,omitempty" jsonschema:"at most this many results (default 20, at most 100)"`
	}
	searchHit struct {
		ThreadID    string     `json:"thread_id"`
		ThreadTitle string     `json:"thread_title"`
		Seq         int64      `json:"seq"`
		Author      string     `json:"author"`
		Time        *time.Time `json:"time,omitempty"`
		Kind        string     `json:"kind"`
		Snippet     string     `json:"snippet" jsonschema:"the matching passage, matched words in **"`
	}
	searchOut struct {
		Results []searchHit `json:"results"`
	}
	prIn struct {
		ThreadID string `json:"thread_id,omitempty" jsonschema:"thread whose pull request to read (default: this thread)"`
		Refresh  bool   `json:"refresh,omitempty" jsonschema:"fetch it from the forge now instead of Banda's latest copy"`
	}
	prCommentIn struct {
		Body    string `json:"body" jsonschema:"the comment, in markdown"`
		ReplyTo string `json:"reply_to,omitempty" jsonschema:"id of a review comment to reply to in its thread"`
	}
)

// pullRequestText renders a pull request for agents.
func pullRequestText(pr *bandav1.PullRequest) string {
	s := pr.Summary
	word := func(e fmt.Stringer, prefix string) string {
		return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(e.String(), prefix)), "_", " ")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Pull request #%d %q (%s): %s\n%s wants to merge %s into %s.\nState: %s; checks: %s; review: %s; mergeable: %s",
		s.Number, s.Title, s.Forge, s.Url, pr.Author, pr.HeadBranch, pr.BaseBranch, word(s.State, "PULL_STATE_"),
		word(s.Checks, "CHECKS_STATE_"), word(s.Review, "REVIEW_STATE_"), word(s.Mergeable, "MERGEABLE_"))
	if pr.MergeDetail != "" {
		b.WriteString(" (" + pr.MergeDetail + ")")
	}
	if pr.Error != "" {
		fmt.Fprintf(&b, "\nThe latest refresh failed (%s); this is as of %s.", pr.Error, pr.FetchedAt.AsTime().Format(time.RFC3339))
	}
	if len(pr.Labels) > 0 {
		b.WriteString("\nLabels: " + strings.Join(pr.Labels, ", "))
	}
	checks := slices.Clone(pr.Checks)
	slices.SortStableFunc(checks, func(a, c *bandav1.PullRequestCheck) int {
		failing := func(c *bandav1.PullRequestCheck) bool { return c.State == bandav1.ChecksState_CHECKS_STATE_FAILING }
		return cmp.Compare(b2i(!failing(a)), b2i(!failing(c)))
	})
	if len(checks) > 0 {
		b.WriteString("\n\nChecks:")
		for _, c := range checks {
			fmt.Fprintf(&b, "\n- %s: %s %s", c.Name, c.Status, c.Url)
		}
	}
	if len(pr.Reviewers) > 0 {
		b.WriteString("\n\nReviewers:")
		for _, r := range pr.Reviewers {
			fmt.Fprintf(&b, "\n- %s: %s", r.Login, word(r.State, "REVIEW_STATE_"))
		}
	}
	fmt.Fprintf(&b, "\n\nDescription:\n%s\n\nConversation (oldest first):", cmp.Or(pr.Body, "(none)"))
	for _, c := range pr.Comments {
		fmt.Fprintf(&b, "\n\n[%s] %s", c.Id, c.Author)
		switch {
		case c.Review != bandav1.ReviewState_REVIEW_STATE_UNSPECIFIED:
			b.WriteString(" reviewed: " + word(c.Review, "REVIEW_STATE_"))
		case c.Path != "" && c.ReplyTo != "":
			fmt.Fprintf(&b, " replied on %s:%d (to %s)", c.Path, c.Line, c.ReplyTo)
		case c.Path != "":
			fmt.Fprintf(&b, " commented on %s:%d", c.Path, c.Line)
		}
		if c.Body != "" {
			b.WriteString(":\n" + c.Body)
		}
	}
	return b.String()
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// mcpTools collects a server's tools, so that its instructions name them all.
type mcpTools struct {
	names []string
	add   []func(*mcp.Server)
}

// addTool adds a tool whose errors reach the agent as they would a client
// (publicError).
func addTool[In, Out any](ts *mcpTools, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	ts.names = append(ts.names, t.Name)
	ts.add = append(ts.add, func(srv *mcp.Server) {
		mcp.AddTool(srv, t, func(ctx context.Context, r *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
			res, out, err := h(ctx, r, in)
			return res, out, publicError(ctx, "mcp/"+t.Name, err)
		})
	})
}

func (s *Service) mcpServer(caller store.Thread) *mcp.Server {
	ts := &mcpTools{}

	addTool(ts, &mcp.Tool{Name: "list_threads", Description: "List Banda threads (each one is a conversation with a coding agent in its own sandbox), most recently active first, at most 50. Every thread is visible to everyone."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, listOut, error) {
			owner := in.Owner
			switch owner {
			case "me":
				owner = caller.OwnerID
			case "all":
				owner = ""
			}
			threads, err := s.store.Recent(ctx, owner, caller.OwnerID, maxListed)
			if err != nil {
				return nil, listOut{}, err
			}
			return nil, s.threadInfos(ctx, threads), nil
		})

	addTool(ts, &mcp.Tool{Name: "list_children", Description: "List the sub-threads spawned from a thread (by create_thread or by a person), most recently active first. Their finished turns and failures are also reported in the parent thread's chat."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in childrenIn) (*mcp.CallToolResult, listOut, error) {
			threads, err := s.store.Children(ctx, or(in.ThreadID, caller.ID), caller.OwnerID)
			if err != nil {
				return nil, listOut{}, err
			}
			return nil, s.threadInfos(ctx, threads), nil
		})

	addTool(ts, &mcp.Tool{Name: "read_thread", Description: "Read a thread (this one too): its metadata, the other active threads whose diffs change the same files, and a compact transcript of its most recent events (messages with their authors, agent text, tool names, turn ends with errors, notices). Optionally include its current git diff."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, any, error) {
			t, err := s.store.Get(ctx, or(in.ThreadID, caller.ID))
			if err != nil {
				return nil, nil, err
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			// ponytail: about half of the events make a line (results, setup steps don't); a turn
			// of mostly subagent calls can leave fewer than limit lines.
			events, err := s.store.Before(ctx, t.ID, math.MaxInt64, limit*10)
			if err != nil {
				return nil, nil, err
			}
			names := s.names(ctx)
			var b strings.Builder
			repo := "none"
			if t.RepoURL != "" {
				repo = t.RepoURL + ", branch " + t.Branch
			}
			fmt.Fprintf(&b, "Thread %s: %s\nOwner: %s (%s)\nStatus: %s, mode: %s, profile: %s\nRepo: %s\nUpdated: %s\n\nTranscript:\n%s\n",
				t.ID, t.Title, actorName(names, t.OwnerID), t.OwnerID, t.Status, t.Mode, t.Profile, repo,
				t.UpdatedAt.Format(time.RFC3339), transcript(names, events, limit))
			overlaps, err := s.overlaps(ctx, t.ID)
			if err != nil {
				return nil, nil, err
			}
			if len(overlaps[t.ID]) > 0 {
				b.WriteString("\nOverlaps (other active threads on this repository and base branch that change the same files; coordinate with their agents and owners):\n")
				for _, o := range overlaps[t.ID] {
					fmt.Fprintf(&b, "- %s %q by %s (%s): %s\n", o.ThreadId, o.Title, actorName(names, o.OwnerId),
						strings.ToLower(strings.TrimPrefix(o.Status.String(), "STATUS_")), strings.Join(o.Paths, ", "))
				}
			}
			if in.IncludeDiff {
				d := t.Diff
				if len(d) > maxDiff {
					d = d[:maxDiff] + "\n… diff truncated at 20 KB"
				}
				fmt.Fprintf(&b, "\nDiff:\n%s\n", d)
			}
			return textResult(b.String()), nil, nil
		})

	addTool(ts, &mcp.Tool{Name: "search_threads", Description: "Full-text search across every Banda thread: what people wrote (messages, review comments), what agents said, and their tool calls (commands, file paths). Use it to find prior work and decisions, e.g. how something was set up before. Best matches first; read a hit's thread with read_thread."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
			hits, err := s.store.Search(ctx, store.SearchQuery{Words: in.Query, Kind: in.Kind, Limit: cmp.Or(in.Limit, 20)})
			if err != nil {
				return nil, searchOut{}, err
			}
			names, out := s.names(ctx), searchOut{Results: []searchHit{}}
			for _, h := range hits {
				out.Results = append(out.Results, searchHit{h.ThreadID, h.Title, h.Seq, actorName(names, h.Actor), h.Time, h.Kind, snippetText(h.Snippet)})
			}
			return nil, out, nil
		})

	addTool(ts, &mcp.Tool{Name: "post_message", Description: "Post a message into another thread, as this thread's agent. Without mention_agent the message only goes into the thread's chat for its people and agent to read, and the thread switches to chat mode. With mention_agent the thread's agent starts working on it; that is only allowed in threads owned by the same person as this thread, since the turn runs on their Claude subscription. You cannot post into this thread itself. Without mention_agent, that thread's agent later sees your message quoted as data from another agent, not as instructions."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in postIn) (*mcp.CallToolResult, any, error) {
			if in.ThreadID == caller.ID {
				return nil, nil, connect.NewError(connect.CodeInvalidArgument, "cannot post into your own thread")
			}
			if in.MentionAgent {
				t, err := s.store.Get(ctx, in.ThreadID)
				if err != nil {
					return nil, nil, err
				}
				if t.CheckOwner(caller.OwnerID) != nil {
					return nil, nil, connect.NewError(connect.CodePermissionDenied, "mention_agent is only allowed in threads owned by the same person as this thread")
				}
			}
			_, err := s.SendMessage(WithUser(ctx, "agent:"+caller.ID), &bandav1.SendMessageRequest{ThreadId: in.ThreadID,
				Message: &bandav1.Message{Text: in.Text, MentionsAgent: in.MentionAgent}})
			if err != nil {
				return nil, nil, err
			}
			return textResult("Message posted."), nil, nil
		})

	addTool(ts, &mcp.Tool{Name: "create_thread", Description: "Start a sub-thread of this thread: a fresh coding agent in its own sandbox and branch that begins working on the prompt, owned by the same person as this thread. Use it to split work into parallel parts. Repository, model, effort and profile default to this thread's; it starts from this thread's branch if that was pushed (push first to hand over your commits), else from this thread's base branch. When it finishes a turn or fails, a line appears in this thread's chat; read details with read_thread and list sub-threads with list_children. " +
		"With wake_parent such a report also starts a turn of yours: the first one always, a later one only if a person posted in either thread since. " +
		fmt.Sprintf("Limits: sub-threads nest at most %d deep, and at most %d sub-threads started by agents run at once per owner (until they pause when idle); beyond them the call fails and you should do the work yourself.", s.MaxSubthreadDepth, s.MaxAgentThreads)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, createOut, error) {
			if err := s.checkFanOut(ctx, caller); err != nil {
				return nil, createOut{}, err
			}
			child, err := s.spawn(ctx, caller, caller.OwnerID, "agent", &bandav1.Message{Text: in.Prompt}, in.RepoURL, in.BaseBranch, in.Model, in.Effort, in.Profile, in.WakeParent)
			if err != nil {
				return nil, createOut{}, err
			}
			return nil, createOut{child.ID}, nil
		})

	addTool(ts, &mcp.Tool{Name: "handoff_thread", Description: "Offer this thread to a teammate, for the person you work for, when they asked you to hand it over: its next turns would run on the teammate's Claude subscription with their secrets, in a new Claude Code session that gets the conversation and your briefing. " +
		"It only posts the offer in this thread's chat; the teammate accepts or declines it, and your owner can cancel it. One offer at a time."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in handoffIn) (*mcp.CallToolResult, any, error) {
			if strings.TrimSpace(in.Briefing) == "" {
				return nil, nil, connect.NewError(connect.CodeInvalidArgument, "a briefing is required")
			}
			to, err := s.teammate(ctx, in.To)
			if err != nil {
				return nil, nil, err
			}
			if err := s.offer(ctx, caller.ID, caller.OwnerID, "agent:"+caller.ID, to, in.Briefing); err != nil {
				return nil, nil, err
			}
			return textResult("Offered this thread to " + actorName(s.names(ctx), to) + "; they still have to accept it."), nil, nil
		})

	addTool(ts, &mcp.Tool{Name: "read_pull_request", Description: "Read a thread's pull request (GitLab: merge request), this thread's by default: its state, checks with their status and links (failing first), reviewers and their verdicts, mergeability, description and the conversation, review comments with their file and line. Use it to act on failing checks and review comments. Banda refreshes it from the forge every few minutes; refresh fetches it now."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in prIn) (*mcp.CallToolResult, any, error) {
			res, err := s.PullRequestGet(WithUser(ctx, caller.OwnerID), &bandav1.PullRequestGetRequest{ThreadId: or(in.ThreadID, caller.ID), Refresh: in.Refresh})
			if err != nil {
				return nil, nil, err
			}
			if res.PullRequest == nil {
				return textResult("This thread has no pull request."), nil, nil
			}
			return textResult(pullRequestText(res.PullRequest)), nil, nil
		})

	addTool(ts, &mcp.Tool{Name: "comment_pull_request", Description: "Comment on this thread's own pull request, or reply to one of its review comments (reply_to: the comment's id from read_pull_request). It is posted with the thread owner's forge token, so it appears as theirs: say that you are their agent."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in prCommentIn) (*mcp.CallToolResult, any, error) {
			if _, err := s.PullRequestComment(WithUser(ctx, caller.OwnerID), &bandav1.PullRequestCommentRequest{ThreadId: caller.ID, Body: in.Body, ReplyTo: in.ReplyTo}); err != nil {
				return nil, nil, err
			}
			return textResult("Comment posted."), nil, nil
		})

	srv := mcp.NewServer(&mcp.Implementation{Name: "banda", Version: "1"}, &mcp.ServerOptions{SchemaCache: mcpSchemas,
		Instructions: "Banda's tools (" + strings.Join(ts.names, ", ") + ") let you see the team's other threads, search them, " +
			"post into them, start sub-threads of this one and offer it to a teammate."})
	for _, add := range ts.add {
		add(srv)
	}
	return srv
}

// teammate is the id of the user with that id or (case-insensitively) name.
func (s *Service) teammate(ctx context.Context, idOrName string) (string, error) {
	var found []string
	for id, name := range s.names(ctx) {
		if id == idOrName {
			return id, nil
		}
		if strings.EqualFold(name, strings.TrimSpace(idOrName)) {
			found = append(found, id)
		}
	}
	if len(found) != 1 {
		return "", connect.NewError(connect.CodeInvalidArgument, "no such teammate, or several by that name: use their user id")
	}
	return found[0], nil
}

// transcript renders the last limit lines of a thread's history: messages,
// agent text, tool names, turn ends and notices. Everything else is left out.
func transcript(names map[string]string, events []*bandav1.Event, limit int) string {
	var lines []string
	for _, e := range events {
		switch k := e.Kind.(type) {
		case *bandav1.Event_Message:
			lines = append(lines, actorName(names, e.Actor)+": "+messageText(k.Message)+attached(k.Message))
		case *bandav1.Event_LineageUpdate:
			lines = append(lines, lineageLine(k.LineageUpdate))
		case *bandav1.Event_Overlap:
			lines = append(lines, overlapLine(names, k.Overlap))
		case *bandav1.Event_PullRequestChanged:
			lines = append(lines, pullRequestLine(k.PullRequestChanged))
		case *bandav1.Event_TurnRequest:
			lines = append(lines, actorName(names, e.Actor)+" (asks the agent, needs the owner's approval): "+messageText(k.TurnRequest.GetMessage())+attached(k.TurnRequest.GetMessage()))
		case *bandav1.Event_TurnRequestResolved:
			line := actorName(names, e.Actor) + " approved a request"
			if r := k.TurnRequestResolved; !r.Approved {
				line = strings.TrimSuffix(actorName(names, e.Actor)+" declined a request: "+r.Note, ": ")
			}
			lines = append(lines, line)
		case *bandav1.Event_AgentText:
			lines = append(lines, "agent: "+k.AgentText.Text)
		case *bandav1.Event_ToolCall:
			if k.ToolCall.ParentToolCallId == "" { // a subagent's calls are summed up by its Task call
				lines = append(lines, "agent called tool "+k.ToolCall.Name)
			}
		case *bandav1.Event_TurnEnded:
			line := "turn ended"
			if k.TurnEnded.IsError {
				line += " with error: " + k.TurnEnded.Error
			}
			lines = append(lines, line)
		case *bandav1.Event_Notice:
			lines = append(lines, "notice: "+k.Notice.Text)
		}
	}
	return strings.Join(lines[max(0, len(lines)-limit):], "\n")
}

// attached names a message's attachments for the transcript.
func attached(m *bandav1.Message) string {
	var s string
	for _, a := range m.GetAttachments() {
		s += " [attached: " + a.Name + "]"
	}
	return s

}

func (s *Service) threadInfos(ctx context.Context, threads []store.Thread) listOut {
	names, out := s.names(ctx), listOut{Threads: make([]threadInfo, 0, len(threads))}
	for _, t := range threads {
		out.Threads = append(out.Threads, threadInfo{t.ID, t.Title, t.OwnerID, actorName(names, t.OwnerID),
			string(t.Status), string(t.Mode), t.RepoURL, t.Branch, t.UpdatedAt, t.ParentID})
	}
	return out
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
