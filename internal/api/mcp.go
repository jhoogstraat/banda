package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
		t, err := s.store.Get(r.Context(), r.Header.Get("X-Banda-Thread"))
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Error("mcp auth", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if err != nil || subtle.ConstantTimeCompare([]byte(t.RunnerToken), []byte(token)) != 1 {
			http.Error(w, "unknown thread or bad token", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, t)))
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
}

type (
	listIn struct {
		Owner string `json:"owner,omitempty" jsonschema:"'me' for threads owned by the same person as this thread, 'all' (default) for everyone's, or a user id"`
	}
	listOut struct {
		Threads []threadInfo `json:"threads"`
	}
	readIn struct {
		ThreadID    string `json:"thread_id" jsonschema:"id of the thread to read"`
		Limit       int    `json:"limit,omitempty" jsonschema:"how many of the most recent transcript lines to return (default 50)"`
		IncludeDiff bool   `json:"include_diff,omitempty" jsonschema:"also return the thread's git diff, truncated to 20 KB"`
	}
	postIn struct {
		ThreadID     string `json:"thread_id" jsonschema:"id of the thread to post into"`
		Text         string `json:"text" jsonschema:"the message"`
		MentionAgent bool   `json:"mention_agent,omitempty" jsonschema:"make that thread's agent act on the message; only allowed for threads owned by the same person as this thread"`
	}
	createIn struct {
		Prompt     string `json:"prompt" jsonschema:"the task for the new thread's agent"`
		RepoURL    string `json:"repo_url,omitempty" jsonschema:"git repository to clone (default: this thread's)"`
		BaseBranch string `json:"base_branch,omitempty" jsonschema:"branch to start from (default: this thread's)"`
		Model      string `json:"model,omitempty" jsonschema:"model (default: this thread's)"`
		Effort     string `json:"effort,omitempty" jsonschema:"effort (default: this thread's)"`
	}
	createOut struct {
		ThreadID string `json:"thread_id"`
	}
)

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func (s *Service) mcpServer(caller store.Thread) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "banda", Version: "1"}, &mcp.ServerOptions{SchemaCache: mcpSchemas})

	mcp.AddTool(srv, &mcp.Tool{Name: "list_threads", Description: "List Banda threads (each one is a conversation with a coding agent in its own sandbox), most recently active first, at most 50. Every thread is visible to everyone."},
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
			names, out := s.names(ctx), listOut{Threads: make([]threadInfo, 0, len(threads))}
			for _, t := range threads {
				out.Threads = append(out.Threads, threadInfo{t.ID, t.Title, t.OwnerID, actorName(names, t.OwnerID),
					string(t.Status), string(t.Mode), t.RepoURL, t.Branch, t.UpdatedAt})
			}
			return nil, out, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "read_thread", Description: "Read another thread: its metadata and a compact transcript of its most recent events (messages with their authors, agent text, tool names, turn ends with errors, notices). Optionally include its current git diff."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, any, error) {
			t, err := s.store.Get(ctx, in.ThreadID)
			if err != nil {
				return nil, nil, err
			}
			// ponytail: reads the whole history; add a LIMITed store query when threads get long.
			events, err := s.store.Events(ctx, t.ID, 0)
			if err != nil {
				return nil, nil, err
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			names := s.names(ctx)
			var b strings.Builder
			fmt.Fprintf(&b, "Thread %s: %s\nOwner: %s (%s)\nStatus: %s, mode: %s\nRepo: %s, branch %s\nUpdated: %s\n\nTranscript:\n%s\n",
				t.ID, t.Title, actorName(names, t.OwnerID), t.OwnerID, t.Status, t.Mode, t.RepoURL, t.Branch,
				t.UpdatedAt.Format(time.RFC3339), transcript(names, events, limit))
			if in.IncludeDiff {
				d := t.Diff
				if len(d) > maxDiff {
					d = d[:maxDiff] + "\n… diff truncated at 20 KB"
				}
				fmt.Fprintf(&b, "\nDiff:\n%s\n", d)
			}
			return textResult(b.String()), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "post_message", Description: "Post a message into another thread, as this thread's agent. Without mention_agent the message only goes into the thread's chat for its people and agent to read, and the thread switches to chat mode. With mention_agent the thread's agent starts working on it; that is only allowed in threads owned by the same person as this thread, since the turn runs on their Claude subscription. You cannot post into this thread itself."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in postIn) (*mcp.CallToolResult, any, error) {
			if in.ThreadID == caller.ID {
				return nil, nil, errors.New("cannot post into your own thread")
			}
			if in.MentionAgent {
				t, err := s.store.Get(ctx, in.ThreadID)
				if err != nil {
					return nil, nil, err
				}
				if t.OwnerID != caller.OwnerID {
					return nil, nil, errors.New("mention_agent is only allowed in threads owned by the same person as this thread")
				}
			}
			_, err := s.SendMessage(WithUser(ctx, "agent:"+caller.ID), &bandav1.SendMessageRequest{ThreadId: in.ThreadID,
				Message: &bandav1.Message{Text: in.Text, MentionsAgent: in.MentionAgent}})
			if err != nil {
				return nil, nil, err
			}
			return textResult("Message posted."), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "create_thread", Description: "Start a new thread: a fresh coding agent in its own sandbox that begins working on the prompt, owned by the same person as this thread. Repository, base branch, model and effort default to this thread's. Follow its progress with read_thread."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, createOut, error) {
			res, err := s.CreateThread(WithUser(ctx, caller.OwnerID), &bandav1.CreateThreadRequest{Prompt: in.Prompt,
				RepoUrl: or(in.RepoURL, caller.RepoURL), BaseBranch: or(in.BaseBranch, caller.BaseBranch),
				Model: or(in.Model, caller.Model), Effort: or(in.Effort, caller.Effort)})
			if err != nil {
				return nil, createOut{}, err
			}
			return nil, createOut{res.Thread.Id}, nil
		})
	return srv
}

// transcript renders the last limit lines of a thread's history: messages,
// agent text, tool names, turn ends and notices. Everything else is left out.
func transcript(names map[string]string, events []*bandav1.Event, limit int) string {
	var lines []string
	for _, e := range events {
		switch k := e.Kind.(type) {
		case *bandav1.Event_Message:
			lines = append(lines, actorName(names, e.Actor)+": "+messageText(k.Message))
		case *bandav1.Event_TurnRequest:
			lines = append(lines, actorName(names, e.Actor)+" (asks the agent, needs the owner's approval): "+messageText(k.TurnRequest.GetMessage()))
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
