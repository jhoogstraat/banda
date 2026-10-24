//go:build contract

// Contract tests: the runner's mapping (translate, permissions) against the
// real `claude` pinned in the sandbox image, driven by a scripted fake
// Messages API. `mise run contract` runs them inside that image with no
// network, a throwaway API key and an empty CLAUDE_CONFIG_DIR: no Claude login
// and no Anthropic. Never run them on a machine with a Claude login.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// apiReq is a request to the fake Messages API.
type apiReq struct {
	Model        string          `json:"model"`
	Stream       bool            `json:"stream"`
	System       json.RawMessage `json:"system"`
	OutputConfig json.RawMessage `json:"output_config"`
	Messages     []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	} `json:"tools"`
}

// last is what the request answers: "result:<tool_use_id>" for a tool result,
// else the text of the last user message (the CLI may append a system one).
func (r *apiReq) last() string {
	var raw json.RawMessage
	for _, m := range r.Messages {
		if m.Role == "user" {
			raw = m.Content
		}
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		var s string
		json.Unmarshal(raw, &s)
		return s
	}
	var texts []string
	for _, b := range slices.Backward(blocks) {
		if b.Type == "tool_result" {
			return "result:" + b.ToolUseID
		}
		if b.Type == "text" {
			texts = append(texts, b.Text)
		}
	}
	slices.Reverse(texts)
	return strings.Join(texts, "\n")
}

// schema is the input schema of an offered tool, "" if it isn't offered.
func (r *apiReq) schema(name string) string {
	for _, t := range r.Tools {
		if t.Name == name {
			return string(t.InputSchema)
		}
	}
	return ""
}

func textBlock(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
func thinkingBlock(s string) map[string]any {
	return map[string]any{"type": "thinking", "thinking": s}
}

// apiError is a script reply that fails the request with this status and
// error type; status 0 drops the connection instead.
func apiError(status int, typ string) map[string]any {
	return map[string]any{"status": status, "error": typ}
}
func toolUse(id, name, input string) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": json.RawMessage(input)}
}

// fakeAPI is a Messages API whose replies come from script, and Banda's MCP
// server as a stub (/mcp) that records its calls.
type fakeAPI struct {
	*httptest.Server
	script func(r *apiReq) []map[string]any // nil: the default reply
	mu     sync.Mutex
	reqs   []*apiReq
	mcp    []string // tools called and their Authorization and X-Banda-Thread headers
	// limitedUntil, when set, answers every request as Anthropic does at a
	// usage limit: 429 with the unified rate-limit headers.
	limitedUntil time.Time
}

func newFakeAPI(t *testing.T, script func(r *apiReq) []map[string]any) *fakeAPI {
	f := &fakeAPI{script: script}
	srv := mcp.NewServer(&mcp.Implementation{Name: "banda", Version: "1"}, nil)
	for _, name := range []string{"list_threads", "create_thread"} {
		srv.AddTool(&mcp.Tool{Name: name, Description: "Stub of Banda's " + name, InputSchema: json.RawMessage(`{"type":"object"}`)},
			func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "stub " + name + " " + string(r.Params.Arguments)}}}, nil
			})
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/mcp":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"tools/call"`) {
				f.mu.Lock()
				f.mcp = append(f.mcp, string(body)+" auth="+r.Header.Get("Authorization")+" thread="+r.Header.Get("X-Banda-Thread"))
				f.mu.Unlock()
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			mcpHandler.ServeHTTP(w, r)
		case r.URL.Path == "/v1/messages":
			f.messages(w, r)
		default: // e.g. HEAD /api/hello, a reachability check
		}
	}))
	t.Cleanup(f.Close)
	t.Setenv("ANTHROPIC_BASE_URL", f.URL)
	t.Setenv("BANDA_API_URL", f.URL)
	return f
}

func (f *fakeAPI) messages(w http.ResponseWriter, r *http.Request) {
	var req apiReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, &req)
	n := len(f.reqs)
	until := f.limitedUntil
	f.mu.Unlock()
	if !until.IsZero() {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("anthropic-ratelimit-unified-status", "rejected")
		h.Set("anthropic-ratelimit-unified-reset", fmt.Sprint(until.Unix()))
		h.Set("anthropic-ratelimit-unified-representative-claim", "five_hour")
		h.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprint(until.Unix()))
		h.Set("anthropic-ratelimit-unified-5h-utilization", "1.0")
		h.Set("anthropic-ratelimit-unified-overage-status", "rejected")
		h.Set("anthropic-ratelimit-unified-overage-disabled-reason", "org_level_disabled")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"Error"},"request_id":"req_fake"}`)
		return
	}
	var content []map[string]any
	if f.script != nil {
		content = f.script(&req)
	}
	if content == nil {
		content = []map[string]any{textBlock("ok")}
	}
	if status, ok := content[0]["status"].(int); ok && status == 0 {
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
		return
	} else if ok { // an error reply: see apiError
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": content[0]["error"], "message": content[0]["error"]}})
		return
	}
	stop := "end_turn"
	for _, b := range content {
		if b["type"] == "tool_use" {
			stop = "tool_use"
		}
	}
	id := fmt.Sprintf("msg_fake%d", n)
	usage := map[string]any{"input_tokens": 10, "output_tokens": 5}
	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model,
			"content": content, "stop_reason": stop, "stop_sequence": nil, "usage": usage})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	ev := func(v map[string]any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v["type"], b)
	}
	ev(map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model,
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": usage}})
	for i, b := range content {
		switch b["type"] {
		case "text":
			ev(map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "text", "text": ""}})
			ev(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": b["text"]}})
		case "thinking":
			ev(map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
			ev(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "thinking_delta", "thinking": b["thinking"]}})
			ev(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "signature_delta", "signature": "sigfake"}})
		case "tool_use":
			ev(map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": b["id"], "name": b["name"], "input": map[string]any{}}})
			ev(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(b["input"].(json.RawMessage))}})
		}
		ev(map[string]any{"type": "content_block_stop", "index": i})
	}
	ev(map[string]any{"type": "message_delta", "delta": map[string]any{"type": "message_delta", "stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 5}})
	ev(map[string]any{"type": "message_stop"})
}

func (f *fakeAPI) requests() []*apiReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// session is one claude process started by the runner's own startClaude.
type session struct {
	t     *testing.T
	c     *claudeProc
	perms *permissions
	lines chan []byte
	raw   []string // every line claude wrote, for failure messages
}

// contractEnv gives claude a throwaway key and an empty config dir, and the
// runner's globals a fresh checkout. newFakeAPI points claude at the fake.
func contractEnv(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("BANDA_CONTRACT") != "1" {
		t.Skip("runs in the sandbox image only: mise run contract")
	}
	for _, k := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-contract-throwaway")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("BANDA_RUNNER_TOKEN", "runner-token")
	threadID = "contract-thread"
	w := t.TempDir()
	old := work
	work = w
	t.Cleanup(func() { work = old })
}

func start(t *testing.T, s settings) *session {
	ctx, cancel := context.WithCancel(context.Background())
	c, err := startClaude(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	ss := &session{t: t, c: c, perms: newPermissions(), lines: make(chan []byte, 1000)}
	go func() {
		defer close(ss.lines)
		out := bufio.NewReaderSize(c.stdout, 1<<20)
		for {
			raw, err := out.ReadBytes('\n')
			if len(raw) > 0 {
				ss.lines <- raw
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		ss.stop()
		cancel()
		if t.Failed() {
			t.Log("claude's output:\n" + strings.Join(ss.raw, ""))
		}
	})
	return ss
}

// stop closes stdin and waits for claude to exit, as the runner does before a restart.
func (s *session) stop() {
	s.c.stdin.Close()
	for raw := range s.lines {
		s.raw = append(s.raw, string(raw))
	}
	s.c.cmd.Wait()
}

func (s *session) write(v any) {
	if err := s.c.write(v); err != nil {
		s.t.Fatal(err)
	}
}

// setMode sends what the runner sends when the API sets the thread's mode.
func (s *session) setMode(m bandav1.PermissionMode) {
	for _, l := range s.perms.apply(&bandav1.PermissionCommand{Mode: m}) {
		s.write(l)
	}
}

// answer decides a permission request; nil leaves it pending.
type answer func(r *bandav1.PermissionRequested) *bandav1.PermissionCommand

// until relays claude's lines like the runner's loop until n results came,
// and returns the events and the results' raw lines.
func (s *session) until(n int, ans answer) (events []*bandav1.Event, results []string) {
	timeout := time.After(90 * time.Second)
	for len(results) < n {
		var raw []byte
		select {
		case r, ok := <-s.lines:
			if !ok {
				s.t.Fatalf("claude exited after %d of %d results", len(results), n)
			}
			raw = r
		case <-timeout:
			s.t.Fatalf("no result after 90 s (%d of %d)", len(results), n)
		}
		s.raw = append(s.raw, string(raw))
		evs, ended, _ := translate(raw)
		for _, e := range evs {
			if r := e.GetPermissionRequested(); r != nil {
				if auto := s.perms.ask(r); auto != nil {
					s.write(auto)
					continue
				}
				if ans != nil {
					if c := ans(r); c != nil {
						c.RequestId = r.Id
						for _, l := range s.perms.apply(c) {
							s.write(l)
						}
					}
				}
			}
			if r := e.GetPermissionResolved(); r != nil {
				s.perms.drop(r.Id)
			}
			events = append(events, e)
			if m := s.perms.agentMode(e); m != nil {
				events = append(events, m)
			}
		}
		if id, ok := unsupportedRequest(raw); ok {
			s.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": id, "error": "not supported by Banda"}})
		}
		if ended {
			s.perms.clear()
			results = append(results, string(raw))
		}
	}
	return events, results
}

func (s *session) prompt(text string) {
	s.write(userMessage(&bandav1.Prompt{Text: text}))
}

// turn sends a prompt and relays its turn.
func (s *session) turn(text string, ans answer) []*bandav1.Event {
	s.prompt(text)
	events, _ := s.until(1, ans)
	return events
}

func describeAll(events []*bandav1.Event) []string {
	var out []string
	for _, e := range events {
		if d := describe(e); d != "" && !strings.HasPrefix(d, "agent_text_delta:") {
			out = append(out, d)
		}
	}
	return out
}

// want checks that the described events contain each of these, in order.
func want(t *testing.T, events []*bandav1.Event, wanted ...string) {
	t.Helper()
	got := describeAll(events)
	i := 0
	for _, g := range got {
		if i < len(wanted) && strings.HasPrefix(g, wanted[i]) {
			i++
		}
	}
	if i < len(wanted) {
		t.Errorf("missing %q (in order) in:\n%s", wanted[i], strings.Join(got, "\n"))
	}
}

// One turn with thinking, text, a Bash call and a subagent whose own Bash call
// nests in its Agent call.
func TestContractTurn(t *testing.T) {
	contractEnv(t)
	api := newFakeAPI(t, func(r *apiReq) []map[string]any {
		switch l := r.last(); {
		case strings.HasSuffix(l, "scenario one"):
			return []map[string]any{thinkingBlock("Let me look."), textBlock("Running it."), toolUse("toolu_bash1", "Bash", `{"command":"echo hi","description":"Print hi"}`)}
		case l == "result:toolu_bash1": // the model's default: no run_in_background
			return []map[string]any{toolUse("toolu_agent1", "Agent", `{"description":"Look around","prompt":"List the files please","subagent_type":"general-purpose"}`)}
		case strings.HasSuffix(l, "List the files please"):
			return []map[string]any{textBlock("Listing."), toolUse("toolu_sub1", "Bash", `{"command":"ls","description":"List files"}`)}
		case l == "result:toolu_sub1":
			return []map[string]any{textBlock("Two files.")}
		case l == "result:toolu_agent1":
			return []map[string]any{textBlock("Done.")}
		}
		return nil
	})
	s := start(t, settings{})
	s.setMode(bandav1.PermissionMode_PERMISSION_MODE_BYPASS)
	events := s.turn("scenario one", nil)
	want(t, events, "agent_thinking:Let me look.", "agent_text:Running it.", `tool_call:Bash {"command":"echo hi"`, "tool_result:toolu_bash1 hi",
		`tool_call:Agent {"description":"Look around"`, `tool_call:Bash {"command":"ls","description":"List files"} parent=toolu_agent1`, "tool_result:toolu_sub1",
		"tool_result:toolu_agent1", "agent_text:Done.", "turn_ended:false")
	deltas := ""
	for _, e := range events {
		deltas += e.GetAgentTextDelta().GetText()
		if r := e.GetToolResult(); r != nil && r.ToolCallId == "toolu_agent1" && !strings.Contains(r.Output, "Two files.") {
			t.Errorf("the Agent result lacks the subagent's answer: %q", r.Output)
		}
	}
	// The subagent's text and deltas are dropped; its answer is in the Agent result.
	if deltas != "Running it.Done." || slices.Contains(describeAll(events), "agent_text:Two files.") {
		t.Errorf("deltas %q, events:\n%s", deltas, strings.Join(describeAll(events), "\n"))
	}
	// The runner turns background tasks off, so a subagent ends within its turn:
	// a background one would end after it and start a turn of its own.
	r := api.requests()[0]
	if s := r.schema("Agent"); s == "" || strings.Contains(s, "run_in_background") || strings.Contains(r.schema("Bash"), "run_in_background") {
		t.Errorf("Agent's schema: %s", s)
	}
	// -p offers no TodoWrite; the runner's CLAUDE_CODE_ENABLE_TODO_TOOLS=1 offers the task tools (TestContractPlan).
	if r.schema("TodoWrite") != "" || r.schema("Task") != "" || r.schema("TaskCreate") == "" {
		t.Error("TodoWrite or Task is offered again, or TaskCreate isn't: update deploy/fake-claude and docs/research/claude-headless.md")
	}
	if r.schema("mcp__banda__list_threads") == "" {
		t.Error("the banda MCP server's tools are not offered")
	}
}

// The task tools change one task per call; after each, the runner sends the
// whole list from the CLI's files, also after a restart.
func TestContractPlan(t *testing.T) {
	contractEnv(t)
	calls := []map[string]any{
		toolUse("toolu_c1", "TaskCreate", `{"subject":"Fix the parser","description":"The config parser","activeForm":"Fixing the parser"}`),
		toolUse("toolu_c2", "TaskCreate", `{"subject":"Run the tests","description":"All of them","activeForm":"Running the tests"}`),
		toolUse("toolu_u1", "TaskUpdate", `{"taskId":"1","status":"completed"}`),
		toolUse("toolu_u2", "TaskUpdate", `{"taskId":"2","status":"in_progress"}`),
		toolUse("toolu_u3", "TaskUpdate", `{"taskId":"2","status":"deleted"}`),
	}
	newFakeAPI(t, func(r *apiReq) []map[string]any {
		l := r.last()
		switch {
		case strings.HasSuffix(l, "plan"):
			return calls[:1]
		case strings.HasSuffix(l, "drop it"):
			return calls[4:5]
		}
		for i, c := range calls[:3] {
			if l == "result:"+c["id"].(string) {
				return calls[i+1 : i+2]
			}
		}
		return nil
	})
	s := start(t, settings{})
	s.setMode(bandav1.PermissionMode_PERMISSION_MODE_BYPASS)
	want(t, s.turn("plan", nil), "tool_call:TaskCreate", "tool_result:toolu_c1 Task #1 created successfully: Fix the parser",
		"plan_changed:pending Fix the parser (Fixing the parser)",
		"plan_changed:pending Fix the parser (Fixing the parser), pending Run the tests (Running the tests)",
		"plan_changed:completed Fix the parser (Fixing the parser), pending Run the tests (Running the tests)",
		"tool_call:TaskUpdate", "tool_result:toolu_u2 Updated task #2 status",
		"plan_changed:completed Fix the parser (Fixing the parser), in_progress Run the tests (Running the tests)", "turn_ended:false")
	s.stop()
	s = start(t, settings{})
	s.setMode(bandav1.PermissionMode_PERMISSION_MODE_BYPASS)
	want(t, s.turn("drop it", nil), "tool_call:TaskUpdate", "plan_changed:completed Fix the parser (Fixing the parser)", "turn_ended:false")
}

// Failed requests that aren't about the login are retried; each retry is a
// line the chat shows until output resumes. Auth errors keep their own path.
func TestContractRetry(t *testing.T) {
	contractEnv(t)
	failures := []map[string]any{apiError(529, "overloaded_error"), apiError(429, "rate_limit_error"), apiError(500, "api_error"), apiError(0, "")}
	n := 0
	newFakeAPI(t, func(r *apiReq) []map[string]any {
		if n++; n <= len(failures) {
			return failures[n-1 : n]
		}
		return nil
	})
	s := start(t, settings{})
	events := s.turn("retry", nil)
	want(t, events, "agent_retrying:1/10 overloaded 529", "agent_retrying:2/10 rate_limit 429", "agent_retrying:3/10 server_error 500",
		"agent_retrying:4/10 unknown 0", "agent_text:ok", "turn_ended:false")
}

// "/compact" sent as a prompt compacts: one notice, a result that ends the
// prompt, and the next request starts from the summary.
func TestContractCompact(t *testing.T) {
	contractEnv(t)
	api := newFakeAPI(t, func(r *apiReq) []map[string]any {
		if b, _ := json.Marshal(r.Messages); strings.Contains(string(b), "summary of the conversation so far") || strings.Contains(string(b), "Please provide your summary") {
			return []map[string]any{textBlock("<summary>SUMMARY-OF-EARLIER</summary>")}
		}
		return nil
	})
	s := start(t, settings{})
	s.turn("first prompt", nil)
	s.prompt("/compact")
	events, results := s.until(1, nil)
	want(t, events, "notice:Context compacted: the agent now works from a summary", "turn_ended:false")
	if endedPrompts([]byte(results[0])) != 1 || !strings.Contains(results[0], `"num_turns":0`) {
		t.Errorf("the compaction's result: %s", results[0])
	}
	want(t, s.turn("after", nil), "agent_text:ok", "turn_ended:false")
	reqs := api.requests()
	if b, _ := json.Marshal(reqs[len(reqs)-1].Messages); !strings.Contains(string(b), "SUMMARY-OF-EARLIER") || strings.Contains(string(b), "first prompt") {
		t.Errorf("the request after compaction: %.600s", b)
	}
}

// At start the runner asks for the slash commands (initialize): the CLI's own,
// the repo's commands and its skills. Sent as plain prompt text, a command
// and a skill run: the model gets their text, with the arguments.
func TestContractSlashCommands(t *testing.T) {
	contractEnv(t)
	os.MkdirAll(work+"/.claude/commands", 0o755)
	os.MkdirAll(work+"/.claude/skills/greet", 0o755)
	os.WriteFile(work+"/.claude/commands/hello.md", []byte("---\ndescription: Say hello to someone\nargument-hint: <name>\n---\nSay HELLO-CMD to $ARGUMENTS.\n"), 0o644)
	os.WriteFile(work+"/.claude/skills/greet/SKILL.md", []byte("---\nname: greet\ndescription: Greets warmly\n---\nGREET-SKILL-BODY: greet warmly.\n"), 0o644)
	api := newFakeAPI(t, nil)
	s := start(t, settings{})
	events := s.turn("/hello Bob", nil)
	var cmds map[string]*bandav1.SlashCommand
	for _, e := range events {
		if sc := e.GetSlashCommands(); sc != nil {
			cmds = map[string]*bandav1.SlashCommand{}
			for _, c := range sc.Commands {
				cmds[c.Name] = c
			}
		}
	}
	if c := cmds["hello"]; c == nil || c.Description != "Say hello to someone (project)" || c.ArgumentHint != "<name>" || cmds["greet"] == nil || cmds["compact"] == nil {
		t.Errorf("slash commands: %v", cmds)
	}
	s.turn("/greet", nil)
	reqs := api.requests()
	if reqs[0].last() == "/hello Bob" || !strings.Contains(reqs[0].last(), "Say HELLO-CMD to Bob.") || !strings.Contains(reqs[1].last(), "GREET-SKILL-BODY") {
		t.Errorf("the command's or the skill's text didn't reach the model:\n%.300q\n%.300q", reqs[0].last(), reqs[1].last())
	}
}

func TestContractPermissions(t *testing.T) {
	allow := func(r *bandav1.PermissionRequested) *bandav1.PermissionCommand {
		return &bandav1.PermissionCommand{Decision: bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW}
	}
	deny := func(r *bandav1.PermissionRequested) *bandav1.PermissionCommand {
		return &bandav1.PermissionCommand{Decision: bandav1.PermissionDecision_PERMISSION_DECISION_DENY, Message: "Not now"}
	}
	// steps: the calls the model makes one after the other, then "ok".
	steps := func(calls ...map[string]any) func(r *apiReq) []map[string]any {
		return func(r *apiReq) []map[string]any {
			l := r.last()
			if strings.HasSuffix(l, "go") {
				return calls[:1]
			}
			for i, c := range calls[:len(calls)-1] {
				if l == "result:"+c["id"].(string) {
					return calls[i+1 : i+2]
				}
			}
			return nil
		}
	}
	exists := func(t *testing.T, name string, want bool) {
		t.Helper()
		if _, err := os.Stat(work + "/" + name); (err == nil) != want {
			t.Errorf("%s exists: %v, want %v", name, err == nil, want)
		}
	}
	// Each subtest starts with contractEnv, so that work is its checkout.
	run := func(t *testing.T, mode bandav1.PermissionMode, ans answer, calls ...map[string]any) []*bandav1.Event {
		newFakeAPI(t, steps(calls...))
		s := start(t, settings{})
		s.setMode(mode)
		return s.turn("go", ans)
	}
	bash := func(cmd string) map[string]any {
		return toolUse("toolu_bash", "Bash", `{"command":"`+cmd+`","description":"Make a file"}`)
	}

	t.Run("ask allow", func(t *testing.T) {
		contractEnv(t)
		events := run(t, bandav1.PermissionMode_PERMISSION_MODE_ASK, allow, bash("touch allowed.txt"))
		want(t, events, "tool_call:Bash", `permission_requested:Bash {"command":"touch allowed.txt"`, "tool_result:toolu_bash", "turn_ended:false")
		exists(t, "allowed.txt", true)
	})
	t.Run("ask deny", func(t *testing.T) {
		contractEnv(t)
		events := run(t, bandav1.PermissionMode_PERMISSION_MODE_ASK, deny, bash("touch denied.txt"))
		want(t, events, `permission_requested:Bash {"command":"touch denied.txt"`, "tool_result:toolu_bash Not now", "turn_ended:false")
		exists(t, "denied.txt", false)
	})
	t.Run("accept edits", func(t *testing.T) {
		contractEnv(t)
		events := run(t, bandav1.PermissionMode_PERMISSION_MODE_ACCEPT_EDITS, deny,
			toolUse("toolu_w", "Write", `{"file_path":"`+work+`/edit.txt","content":"hi\n"}`),
			toolUse("toolu_py", "Bash", `{"command":"python3 -c 'open(\"py.txt\",\"w\")'","description":"Write with python"}`))
		// The edit runs unasked; a command that isn't an edit asks.
		want(t, events, "tool_call:Write", "tool_result:toolu_w File created", "tool_call:Bash", "permission_requested:Bash", "tool_result:toolu_py Not now")
		if got := describeAll(events); slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "permission_requested:Write") }) {
			t.Errorf("the edit asked:\n%s", strings.Join(got, "\n"))
		}
		exists(t, "edit.txt", true)
		exists(t, "py.txt", false)
	})
	t.Run("plan", func(t *testing.T) {
		contractEnv(t)
		// The CLI tells the model where to write its plan; ExitPlanMode's input
		// is empty and the CLI adds the plan and its path.
		newFakeAPI(t, func(r *apiReq) []map[string]any {
			switch l := r.last(); {
			case strings.HasSuffix(l, "go"):
				b, _ := json.Marshal(r.Messages)
				m := regexp.MustCompile(`create your plan at (\S+\.md)`).FindSubmatch(b)
				if m == nil {
					return []map[string]any{textBlock("no plan file named")}
				}
				return []map[string]any{toolUse("toolu_planfile", "Write", `{"file_path":"`+string(m[1])+`","content":"# Plan\n1. Write plan-done.txt\n"}`)}
			case l == "result:toolu_planfile":
				return []map[string]any{toolUse("toolu_exit", "ExitPlanMode", `{}`)}
			case l == "result:toolu_exit":
				return []map[string]any{toolUse("toolu_w", "Write", `{"file_path":"`+work+`/plan-done.txt","content":"hi\n"}`)}
			}
			return nil
		})
		s := start(t, settings{})
		s.setMode(bandav1.PermissionMode_PERMISSION_MODE_PLAN)
		// Approving the plan as Banda does: allow, then the working mode.
		events := s.turn("go", func(r *bandav1.PermissionRequested) *bandav1.PermissionCommand {
			return &bandav1.PermissionCommand{Decision: bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW, Mode: bandav1.PermissionMode_PERMISSION_MODE_ACCEPT_EDITS}
		})
		want(t, events, "tool_call:Write", "tool_result:toolu_planfile File created", `tool_call:ExitPlanMode {"plan":"# Plan\n1. Write plan-done.txt\n","planFilePath":`,
			`permission_requested:ExitPlanMode {"plan":"# Plan\n1. Write plan-done.txt\n","planFilePath":`, "tool_result:toolu_exit User has approved your plan.",
			"tool_call:Write", "tool_result:toolu_w File created", "turn_ended:false")
		if got := describeAll(events); slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "permission_requested:Write") }) {
			t.Errorf("a write asked:\n%s", strings.Join(got, "\n"))
		}
		exists(t, "plan-done.txt", true)
	})
	t.Run("question", func(t *testing.T) {
		contractEnv(t)
		// Asked even when bypassing permissions; the answers reach the model.
		events := run(t, bandav1.PermissionMode_PERMISSION_MODE_BYPASS, func(r *bandav1.PermissionRequested) *bandav1.PermissionCommand {
			return &bandav1.PermissionCommand{Decision: bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW, Answers: map[string]string{"Which database?": "Postgres", "Which extras?": "Cache, Search"}}
		}, toolUse("toolu_q", "AskUserQuestion", `{"questions":[{"question":"Which database?","header":"Database","options":[{"label":"Postgres","description":"The usual"},{"label":"SQLite","description":"One file"}],"multiSelect":false},`+
			`{"question":"Which extras?","header":"Extras","options":[{"label":"Cache","description":"Faster reads"},{"label":"Search","description":"Full text"}],"multiSelect":true}]}`))
		want(t, events, "tool_call:AskUserQuestion", "permission_requested:AskUserQuestion",
			`tool_result:toolu_q Your questions have been answered: "Which database?"="Postgres", "Which extras?"="Cache, Search".`)
	})
	t.Run("enter plan", func(t *testing.T) {
		contractEnv(t)
		// The agent enters plan mode unasked; a write then asks.
		events := run(t, bandav1.PermissionMode_PERMISSION_MODE_ASK, deny,
			toolUse("toolu_enter", "EnterPlanMode", `{}`), toolUse("toolu_w", "Write", `{"file_path":"`+work+`/x.txt","content":"hi\n"}`))
		want(t, events, "tool_call:EnterPlanMode", "tool_result:toolu_enter Entered plan mode.", "permission_mode_changed:PERMISSION_MODE_PLAN",
			"tool_call:Write", "permission_requested:Write", "tool_result:toolu_w Not now")
		if got := describeAll(events); slices.Contains(got, "permission_requested:EnterPlanMode {}") {
			t.Errorf("EnterPlanMode asked:\n%s", strings.Join(got, "\n"))
		}
	})
}

// A prompt written during a turn joins it at the next tool boundary: one
// result ends both. One written while the model answers without a tool waits
// for a turn of its own. The result's user_message_uuids says which prompts
// it ended; its queued_turn_count stays 0 even with a prompt waiting.
func TestContractFold(t *testing.T) {
	contractEnv(t)
	api := newFakeAPI(t, func(r *apiReq) []map[string]any {
		switch l := r.last(); {
		case strings.HasSuffix(l, "fold first"):
			return []map[string]any{toolUse("toolu_sleep", "Bash", `{"command":"sleep 3","description":"Wait"}`)}
		case l == "result:toolu_sleep":
			return []map[string]any{textBlock("after sleep")}
		case strings.HasSuffix(l, "queue first"):
			time.Sleep(3 * time.Second)
			return []map[string]any{textBlock("slow answer")}
		}
		return nil
	})
	s := start(t, settings{})
	s.setMode(bandav1.PermissionMode_PERMISSION_MODE_BYPASS)
	// ended counts like the runner: the prompts each result ends.
	ended := func(prompts int) (counts []int) {
		for pending := prompts; pending > 0; {
			_, results := s.until(1, nil)
			n := min(endedPrompts([]byte(results[0])), pending)
			if n == 0 {
				t.Fatalf("a result ended no prompt: %s", results[0])
			}
			pending -= n
			counts = append(counts, n)
		}
		return counts
	}

	s.prompt("fold first")
	time.Sleep(1500 * time.Millisecond) // Bash sleeps
	s.prompt("fold second")
	if got := ended(2); !slices.Equal(got, []int{2}) {
		t.Errorf("folded: results ended %v prompts, want [2]", got)
	}
	reqs := api.requests()
	if b, _ := json.Marshal(reqs[len(reqs)-1].Messages); reqs[len(reqs)-1].last() != "result:toolu_sleep" || !strings.Contains(string(b), "fold second") {
		t.Errorf("the second prompt didn't join the tool result's request")
	}

	s.prompt("queue first")
	time.Sleep(1500 * time.Millisecond) // the fake API holds the answer
	s.prompt("queue second")
	if got := ended(2); !slices.Equal(got, []int{1, 1}) {
		t.Errorf("queued: results ended %v prompts, want [1 1]", got)
	}
}

// A changed profile, model and effort restart claude with --resume and
// --system-prompt-snapshot off: the resumed session sends the new ones.
func TestContractResume(t *testing.T) {
	contractEnv(t)
	api := newFakeAPI(t, nil)
	s := start(t, settings{})
	s.turn("first prompt", nil)
	s.stop()
	spec := profileText("spec")
	if r := api.requests()[0]; strings.Contains(string(r.System), spec) {
		t.Fatal("the first start already has the spec profile")
	}

	s = start(t, settings{model: "claude-sonnet-5-5", effort: "low", profile: "spec"})
	if !slices.Contains(s.c.cmd.Args, "--resume") {
		t.Fatalf("not resumed: %q", s.c.cmd.Args)
	}
	s.turn("second prompt", nil)
	reqs := api.requests()
	r := reqs[len(reqs)-1]
	b, _ := json.Marshal(r.Messages)
	var blocks []block
	json.Unmarshal(r.System, &blocks)
	sys := ""
	for _, b := range blocks {
		sys += b.Text
	}
	if r.Model != "claude-sonnet-5-5" || string(r.OutputConfig) != `{"effort":"low"}` || !strings.Contains(string(b), "first prompt") || !strings.Contains(sys, spec) {
		t.Errorf("resumed request: model %s, output_config %s, has the first prompt %v, has the spec profile %v",
			r.Model, r.OutputConfig, strings.Contains(string(b), "first prompt"), strings.Contains(sys, spec))
	}
}

// The agent calls a Banda MCP tool through the runner's --mcp-config: it asks
// first (ask mode), the call carries the runner's token and thread, and the
// result reaches the model.
func TestContractMCP(t *testing.T) {
	contractEnv(t)
	api := newFakeAPI(t, func(r *apiReq) []map[string]any {
		if strings.HasSuffix(r.last(), "go") {
			return []map[string]any{toolUse("toolu_mcp", "mcp__banda__create_thread", `{"prompt":"Split this","wake_parent":true}`)}
		}
		return nil
	})
	s := start(t, settings{})
	s.setMode(bandav1.PermissionMode_PERMISSION_MODE_ASK)
	events := s.turn("go", func(r *bandav1.PermissionRequested) *bandav1.PermissionCommand {
		return &bandav1.PermissionCommand{Decision: bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW}
	})
	want(t, events, "tool_call:mcp__banda__create_thread", "permission_requested:mcp__banda__create_thread",
		`tool_result:toolu_mcp stub create_thread {"prompt":"Split this","wake_parent":true}`, "turn_ended:false")
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.mcp) != 1 || !strings.Contains(api.mcp[0], "auth=Bearer runner-token thread=contract-thread") {
		t.Errorf("MCP calls: %q", api.mcp)
	}
}

// An interrupt ends the turn with an error result that still ends its prompt,
// and claude takes the next prompt.
func TestContractInterrupt(t *testing.T) {
	contractEnv(t)
	held := false
	newFakeAPI(t, func(r *apiReq) []map[string]any {
		if !held {
			held = true
			return []map[string]any{toolUse("toolu_hold", "Bash", `{"command":"sleep 30","description":"Wait"}`)}
		}
		return nil
	})
	s := start(t, settings{})
	s.setMode(bandav1.PermissionMode_PERMISSION_MODE_BYPASS)
	s.prompt("hold")
	time.Sleep(1500 * time.Millisecond)
	if err := s.c.interrupt(); err != nil {
		t.Fatal(err)
	}
	events, results := s.until(1, nil)
	if te := events[len(events)-1].GetTurnEnded(); te == nil || !te.IsError || endedPrompts([]byte(results[0])) != 1 || !strings.Contains(results[0], `"terminal_reason":"aborted_tools"`) {
		t.Errorf("interrupted: %s", results[0])
	}
	want(t, s.turn("next", nil), "agent_text:ok", "turn_ended:false")
}

// At a usage limit (a 429 with the unified rate-limit headers) the CLI in
// API-key mode reads none of them: no rate_limit_event, only api_retry lines
// and an api_error result without a reset time. It reads them only when signed
// in to claude.ai, which a contract run never is. If this fails, the CLI now
// reports the limit here: see docs/research/claude-headless.md, usage limits.
func TestContractUsageLimit(t *testing.T) {
	contractEnv(t)
	t.Setenv("CLAUDE_CODE_MAX_RETRIES", "1") // 10 by default, about 3 minutes
	api := newFakeAPI(t, nil)
	api.limitedUntil = time.Now().Add(2 * time.Hour)
	s := start(t, settings{})
	s.prompt("hi")
	events, results := s.until(1, nil)
	if te := events[len(events)-1].GetTurnEnded(); te == nil || !te.IsError || te.Error != "API Error: Request rejected (429) · Error" ||
		!strings.Contains(results[0], `"terminal_reason":"api_error"`) || !strings.Contains(results[0], `"api_error_status":429`) {
		t.Errorf("result: %s", results[0])
	}
	all := strings.Join(s.raw, "")
	if strings.Contains(all, "rate_limit_event") || strings.Contains(all, fmt.Sprint(api.limitedUntil.Unix())) ||
		!strings.Contains(all, `"subtype":"api_retry","attempt":1,"max_retries":1,"retry_delay_ms":`) || !strings.Contains(all, `"error_status":429,"error":"rate_limit"`) {
		t.Errorf("the limit's lines changed:\n%s", all)
	}
}

// With background tasks off, Bash offers no run_in_background and its text
// says nothing about long-running processes. A server the model backgrounds
// with & returns at once and keeps serving after the turn, a following turn
// and claude's exit (the CLI starts each command in its own session and
// doesn't kill it), also when it writes to the inherited stdout (a file the CLI
// deletes). One in the foreground holds the call until Bash's timeout, which
// kills it. See docs/research/claude-headless.md, dev servers.
func TestContractDevServer(t *testing.T) {
	contractEnv(t)
	calls := map[string]string{
		"redirected": `nohup python3 -m http.server 4173 >/tmp/srv.log 2>&1 &`,
		"inherited":  `node -e "require('http').createServer((q,r)=>{console.log('hit',q.url);r.end('node')}).listen(4174)" &`,
		"foreground": `python3 -m http.server 4175`,
	}
	api := newFakeAPI(t, func(r *apiReq) []map[string]any {
		for p, cmd := range calls {
			if strings.HasSuffix(r.last(), p) {
				b, _ := json.Marshal(map[string]any{"command": cmd, "description": "Start the dev server", "timeout": 5000})
				return []map[string]any{toolUse("toolu_"+p, "Bash", string(b))}
			}
		}
		return nil
	})
	// serves waits up to 5 s for the port to answer and the Browser tab's list to have it.
	serves := func(port uint32) bool {
		for range 25 {
			if resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", port)); err == nil {
				resp.Body.Close()
				if slices.ContainsFunc(listening(), func(p *bandav1.PreviewPort) bool { return p.Port == port }) {
					return true
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		return false
	}
	s := start(t, settings{})
	s.setMode(bandav1.PermissionMode_PERMISSION_MODE_BYPASS)
	for _, p := range []string{"redirected", "inherited"} {
		t0 := time.Now()
		want(t, s.turn(p, nil), "tool_call:Bash", "tool_result:toolu_"+p+" (Bash completed with no output)", "turn_ended:false")
		if d := time.Since(t0); d > 4*time.Second {
			t.Errorf("%s: the turn took %s", p, d)
		}
	}
	if !serves(4173) || !serves(4174) {
		t.Error("a backgrounded server doesn't serve after its turn")
	}
	t0 := time.Now()
	want(t, s.turn("foreground", nil), "tool_call:Bash", "tool_result:toolu_foreground Exit code 143\nCommand timed out after 5s", "turn_ended:false")
	if _, err := http.Get("http://localhost:4175/"); time.Since(t0) < 5*time.Second || err == nil {
		t.Errorf("the foreground server: the turn took %s, still serves %v", time.Since(t0), err == nil)
	}
	s.stop() // as the runner restarts claude
	if !serves(4173) || !serves(4174) {
		t.Error("a backgrounded server doesn't serve after claude exits")
	}
	r := api.requests()[0]
	for _, tl := range r.Tools {
		if tl.Name == "Bash" && (strings.Contains(tl.Description, "background") || strings.Contains(string(tl.InputSchema), "run_in_background")) {
			t.Errorf("Bash tells the model about background commands now: update docs/research/claude-headless.md, dev servers:\n%s\n%s", tl.Description, tl.InputSchema)
		}
	}
}
