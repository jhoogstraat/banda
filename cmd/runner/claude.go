package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// line is the subset of Claude Code's stream-json output we use.
// See docs/research/claude-headless.md.
type line struct {
	Type            string  `json:"type"`
	Subtype         string  `json:"subtype"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Message         struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	// result
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	DurationMS   int64   `json:"duration_ms"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	// The prompts still queued: all others written so far ran in this turn or before.
	QueuedTurnCount int `json:"queued_turn_count"`
	// system/api_retry
	Error string `json:"error"`
	// control_request (can_use_tool) and control_cancel_request
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype        string          `json:"subtype"`
		ToolName       string          `json:"tool_name"`
		Input          json.RawMessage `json:"input"`
		ToolUseID      string          `json:"tool_use_id"`
		DecisionReason string          `json:"decision_reason"`
		Description    string          `json:"description"`
	} `json:"request"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// authErrors are api_retry categories that mean the user must log in again.
var authErrors = map[string]bool{"authentication_failed": true, "oauth_org_not_allowed": true, "account_on_hold": true, "billing_error": true}

// translate maps one stream-json line to Banda events. authFailed reports a
// retry loop caused by a bad login, which the caller should interrupt.
// A subagent's (Task tool's) lines carry parent_tool_use_id: only its tool
// calls and results are kept, tagged with the parent call, so the chat can nest
// them. Its prompt, text and deltas are dropped; the Task result has its answer.
func translate(raw []byte) (events []*bandav1.Event, turnEnded, authFailed bool) {
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return nil, false, false
	}
	sub := l.ParentToolUseID != nil
	parent := ""
	if sub {
		parent = *l.ParentToolUseID
	}
	switch l.Type {
	case "stream_event":
		if !sub && l.Event.Type == "content_block_delta" && l.Event.Delta.Type == "text_delta" {
			events = append(events, &bandav1.Event{Kind: &bandav1.Event_AgentTextDelta{AgentTextDelta: &bandav1.AgentTextDelta{Text: l.Event.Delta.Text}}})
		}
	case "assistant", "user":
		var blocks []block
		if json.Unmarshal(l.Message.Content, &blocks) != nil {
			return nil, false, false // plain string content is our own prompt echoed back
		}
		for _, b := range blocks {
			if sub && b.Type != "tool_use" && b.Type != "tool_result" {
				continue
			}
			switch b.Type {
			case "text":
				events = append(events, &bandav1.Event{Kind: &bandav1.Event_AgentText{AgentText: &bandav1.AgentText{Text: b.Text}}})
			case "thinking":
				if b.Thinking != "" {
					events = append(events, &bandav1.Event{Kind: &bandav1.Event_AgentThinking{AgentThinking: &bandav1.AgentThinking{Text: b.Thinking}}})
				}
			case "tool_use":
				events = append(events, &bandav1.Event{Kind: &bandav1.Event_ToolCall{ToolCall: &bandav1.ToolCall{Id: b.ID, Name: b.Name, InputJson: string(b.Input), ParentToolCallId: parent}}})
			case "tool_result":
				events = append(events, &bandav1.Event{Kind: &bandav1.Event_ToolResult{ToolResult: &bandav1.ToolResult{ToolCallId: b.ToolUseID, Output: text(b.Content), IsError: b.IsError}}})
			}
		}
	case "result":
		if sub {
			return nil, false, false
		}
		te := &bandav1.TurnEnded{IsError: l.IsError, DurationMs: l.DurationMS, CostUsd: l.TotalCostUSD}
		if l.IsError {
			te.Error = l.Result
		}
		return []*bandav1.Event{{Kind: &bandav1.Event_TurnEnded{TurnEnded: te}}}, true, false
	case "system":
		return nil, false, l.Subtype == "api_retry" && authErrors[l.Error]
	case "control_request": // the CLI asks the host; other subtypes are answered by the caller
		if r := l.Request; r.Subtype == "can_use_tool" {
			events = append(events, &bandav1.Event{Kind: &bandav1.Event_PermissionRequested{PermissionRequested: &bandav1.PermissionRequested{
				Id: l.RequestID, ToolCallId: r.ToolUseID, ToolName: r.ToolName, InputJson: string(r.Input), Reason: cmp.Or(r.DecisionReason, r.Description)}}})
		}
	case "control_cancel_request":
		events = append(events, &bandav1.Event{Kind: &bandav1.Event_PermissionResolved{PermissionResolved: &bandav1.PermissionResolved{
			Id: l.RequestID, Decision: bandav1.PermissionDecision_PERMISSION_DECISION_CANCELED}}})
	}
	return events, false, false
}

// queuedTurns is how many prompts the CLI still queues after a result.
func queuedTurns(raw []byte) int {
	var l line
	json.Unmarshal(raw, &l)
	return l.QueuedTurnCount
}

// unsupportedRequest reports the id of a control request from the CLI that
// Banda doesn't answer (anything but can_use_tool), so the caller can refuse it.
func unsupportedRequest(raw []byte) (string, bool) {
	var l line
	if json.Unmarshal(raw, &l) != nil || l.Type != "control_request" || l.Request.Subtype == "can_use_tool" {
		return "", false
	}
	return l.RequestID, true
}

// userMessage is the stream-json user message for a prompt: plain text, or a
// text block followed by base64 image blocks (Messages API content).
func userMessage(p *bandav1.Prompt) map[string]any {
	var content any = p.Text
	if len(p.Images) > 0 {
		blocks := []map[string]any{{"type": "text", "text": p.Text}}
		for _, img := range p.Images {
			// []byte marshals as standard base64.
			blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": img.MediaType, "data": img.Data}})
		}
		content = blocks
	}
	return map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}}
}

// text flattens tool_result content, which is a string or a list of blocks.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		} else {
			parts = append(parts, "["+b.Type+"]")
		}
	}
	return strings.Join(parts, "\n")
}

type claudeProc struct {
	cmd    *exec.Cmd
	stdout io.Reader
	mu     sync.Mutex
	stdin  io.WriteCloser
}

func startClaude(ctx context.Context, s settings) (*claudeProc, error) {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages",
		// Permission prompts come to the runner as control requests (permissions.go). The API sets
		// the thread's mode once the runner is ready; bypass must be allowed for it to be one.
		"--permission-mode", "default", "--permission-prompt-tool", "stdio", "--allow-dangerously-skip-permissions"}
	if s.model != "" {
		args = append(args, "--model", s.model)
	}
	if s.effort != "" {
		args = append(args, "--effort", s.effort)
	}
	// Neither flag is restored on --resume, so both are always passed.
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"banda": map[string]any{
		"type": "http", "url": os.Getenv("BANDA_API_URL") + "/mcp",
		"headers": map[string]string{"Authorization": "Bearer " + os.Getenv("BANDA_RUNNER_TOKEN"), "X-Banda-Thread": threadID},
	}}})
	where := "on git branch " + os.Getenv("BANDA_BRANCH")
	if repoURL == "" {
		where = "without a repository: your working directory started empty and lives only in this thread's environment"
	}
	prompt := fmt.Sprintf(
		"You are running inside Banda thread %s %s. Teammates and other agents may post messages into this thread; they appear in your prompt with their name. "+
			"The banda MCP server's tools, listed in its instructions, let you see and search the team's other threads, post into them and start sub-threads.",
		threadID, where)
	if p := profileText(s.profile); p != "" {
		prompt += "\n\n" + p
	}
	// The CLI records the system prompt on a session's first request and resends that record on
	// resume, so without snapshot off a switched profile would never reach the model.
	args = append(args, "--mcp-config", string(mcpConfig), "--append-system-prompt", prompt, "--system-prompt-snapshot", "off")
	sid := sessionID(threadID + os.Getenv("BANDA_SESSION_KEY"))
	if hasTranscript(sid) {
		args = append(args, "--resume", sid)
	} else {
		args = append(args, "--session-id", sid)
	}

	cmd := claudeCmd(ctx, args...)
	cmd.Dir = work
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	return &claudeProc{cmd: cmd, stdout: stdout, stdin: stdin}, cmd.Start()
}

//go:embed profiles
var profiles embed.FS

// profileText is the profile's text, appended to the system prompt: the
// repo's .banda/profiles/<name>.md, else the built-in (the API sends only
// their names). Read at every start, so an edit applies on the next restart;
// through os.Root (see files), so a symlink can't pull the owner's Claude
// login into the prompt.
func profileText(name string) string {
	text, err := profiles.ReadFile("profiles/" + name + ".md")
	if err != nil {
		return ""
	}
	if root, err := os.OpenRoot(work); err == nil {
		if repo, err := root.ReadFile(".banda/profiles/" + name + ".md"); err == nil {
			text = repo
		}
		root.Close()
	}
	return strings.TrimSpace(string(text))
}

// claudeCmd never kills claude outright: a kill during a token refresh logs
// the user out of every pod. The pod's grace period is 90s.
func claudeCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 80 * time.Second
	return cmd
}

// write sends one stream-json line. A malformed line kills claude, so only
// ever write marshalled values.
func (c *claudeProc) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *claudeProc) interrupt() error {
	return c.write(controlRequest(map[string]any{"subtype": "interrupt"}))
}

func controlRequest(req map[string]any) map[string]any {
	return map[string]any{"type": "control_request", "request_id": rand.Text(), "request": req}
}

// sessionID derives a stable UUID from the thread id (and BANDA_SESSION_KEY,
// which changes with each handoff), so a restarted runner resumes the same
// Claude Code session.
func sessionID(thread string) string {
	h := sha256.Sum256([]byte(thread))
	h[6] = h[6]&0x0f | 0x40
	h[8] = h[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func hasTranscript(sid string) bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".claude")
	}
	m, _ := filepath.Glob(filepath.Join(dir, "projects", "*", sid+".jsonl"))
	return len(m) > 0
}
