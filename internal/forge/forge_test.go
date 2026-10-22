package forge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestRepo(t *testing.T) {
	c := Config{Hosts: map[string]string{"git.corp.example": GitLab, "ghe.corp": GitHub}}
	for url, want := range map[string]Repo{
		"https://github.com/o/r.git":        {GitHub, "github.com", "o/r"},
		"git@github.com:o/r.git":            {GitHub, "github.com", "o/r"},
		"ssh://git@github.com/o/r":          {GitHub, "github.com", "o/r"},
		"https://GitLab.com/g/sub/p.git/":   {GitLab, "gitlab.com", "g/sub/p"},
		"https://git.corp.example/team/app": {GitLab, "git.corp.example", "team/app"},
		"https://ghe.corp/o/r":              {GitHub, "ghe.corp", "o/r"},
		"https://example.com/o/r.git":       {},
		"git://e2e-git/plain.git":           {},
		"https://github.com/o":              {},
		"https://github.com/o/r/tree/main":  {}, // not a repository
		"file:///opt/e2e/plain.git":         {},
	} {
		got, ok := c.Repo(url)
		if got != want || ok != (want.Kind != "") {
			t.Errorf("Repo(%q) = %+v, %v; want %+v", url, got, ok, want)
		}
	}
	if _, err := ParseHosts("a=github, b=gitlab"); err != nil {
		t.Error(err)
	}
	if _, err := ParseHosts("a=gitea"); err == nil {
		t.Error("ParseHosts accepted an unknown kind")
	}
}

// fake is a forge API: routes by "METHOD path", recording what was posted.
type fake struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string]any // a body to answer, or func(*http.Request) (int, any)
	posted map[string]map[string]any
	auth   []string
}

func newFake(t *testing.T, routes map[string]any) (*fake, string) {
	f := &fake{t: t, routes: routes, posted: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.EscapedPath()
	f.mu.Lock()
	f.auth = append(f.auth, r.Header.Get("Authorization")+r.Header.Get("Private-Token"))
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		var m map[string]any
		json.Unmarshal(b, &m)
		f.posted[key] = m
	}
	route, ok := f.routes[key]
	f.mu.Unlock()
	status, body := http.StatusOK, route
	if fn, isFn := route.(func(*http.Request) (int, any)); isFn {
		status, body = fn(r)
	}
	if !ok {
		status, body = http.StatusNotFound, map[string]string{"message": "Not Found"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func open(t *testing.T, kind, base string) Forge {
	r := Repo{Kind: kind, Host: "forge.test", Path: "o/r"}
	f, err := Config{TestURL: base}.Open(r, "tok")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

const gh = "/api/v3/repos/o/r"

func TestGitHub(t *testing.T) {
	ctx := context.Background()
	f, base := newFake(t, map[string]any{
		"GET " + gh + "/pulls":  []any{map[string]any{"number": 7}},
		"POST " + gh + "/pulls": map[string]any{"number": 8},
		"GET " + gh + "/pulls/8": map[string]any{"number": 8, "title": "T", "state": "open", "draft": false, "html_url": "https://x/pull/8",
			"mergeable_state": "blocked", "node_id": "PR_1", "user": map[string]any{"login": "alice"},
			"head": map[string]any{"ref": "banda/x", "sha": "abc"}, "base": map[string]any{"ref": "main"},
			"requested_reviewers": []any{map[string]any{"login": "carol"}}, "labels": []any{map[string]any{"name": "bug"}}},
		"GET " + gh + "/commits/abc/check-runs": map[string]any{"total_count": 2, "check_runs": []any{
			map[string]any{"name": "test", "status": "completed", "conclusion": "failure", "details_url": "https://ci/1"},
			map[string]any{"name": "lint", "status": "in_progress"}}},
		"GET " + gh + "/commits/abc/status": map[string]any{"statuses": []any{map[string]any{"context": "deploy", "state": "success"}}},
		"GET " + gh + "/pulls/8/reviews": []any{
			map[string]any{"id": 1, "user": map[string]any{"login": "bob"}, "state": "CHANGES_REQUESTED", "body": "fix it", "submitted_at": "2026-01-01T10:00:00Z"},
			map[string]any{"id": 2, "user": map[string]any{"login": "bob"}, "state": "COMMENTED", "body": "", "submitted_at": "2026-01-01T11:00:00Z"}},
		"GET " + gh + "/issues/8/comments": []any{map[string]any{"id": 5, "user": map[string]any{"login": "dan"}, "body": "hi", "created_at": "2026-01-01T09:00:00Z"}},
		"GET " + gh + "/pulls/8/comments": []any{
			map[string]any{"id": 10, "user": map[string]any{"login": "bob"}, "body": "here", "path": "a.go", "line": 3, "created_at": "2026-01-01T10:00:01Z"},
			map[string]any{"id": 11, "user": map[string]any{"login": "alice"}, "body": "ok", "path": "a.go", "line": 3, "in_reply_to_id": 10, "created_at": "2026-01-01T12:00:00Z"}},
		"GET /api/v3/repos/o/r":                       map[string]any{"allow_merge_commit": false, "allow_squash_merge": true, "allow_rebase_merge": true},
		"PATCH " + gh + "/pulls/8":                    map[string]any{"number": 8},
		"POST /api/graphql":                           map[string]any{"data": map[string]any{}},
		"POST " + gh + "/pulls/8/requested_reviewers": map[string]any{"number": 8},
		"POST " + gh + "/issues/8/comments":           map[string]any{"id": 6},
		"POST " + gh + "/pulls/8/comments":            map[string]any{"id": 12},
		"PUT " + gh + "/pulls/8/merge":                map[string]any{"merged": true},
		"PUT " + gh + "/pulls/9/merge": func(*http.Request) (int, any) {
			return http.StatusMethodNotAllowed, map[string]any{"message": "Pull Request is not mergeable"}
		},
	})
	g := open(t, GitHub, base)
	if n, err := g.Find(ctx, "banda/x"); n != 7 || err != nil {
		t.Fatalf("Find = %d, %v", n, err)
	}
	if n, err := g.Create(ctx, New{Title: "T", Body: "B", Head: "banda/x", Base: "main", Draft: true}); n != 8 || err != nil {
		t.Fatalf("Create = %d, %v", n, err)
	}
	if p := f.posted["POST "+gh+"/pulls"]; p["head"] != "banda/x" || p["draft"] != true || p["body"] != "B" {
		t.Errorf("Create posted %v", p)
	}
	if !strings.HasPrefix(f.auth[0], "Bearer tok") {
		t.Errorf("Authorization %q", f.auth[0])
	}
	pr, err := g.Get(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	s := pr.Summary
	if s.State != bandav1.PullState_PULL_STATE_OPEN || s.Mergeable != bandav1.Mergeable_MERGEABLE_BLOCKED || s.Checks != bandav1.ChecksState_CHECKS_STATE_FAILING ||
		s.Review != bandav1.ReviewState_REVIEW_STATE_CHANGES_REQUESTED || s.Url != "https://x/pull/8" {
		t.Errorf("summary %v", s)
	}
	if len(pr.Checks) != 3 || pr.Checks[1].State != bandav1.ChecksState_CHECKS_STATE_PENDING || pr.Checks[2].State != bandav1.ChecksState_CHECKS_STATE_PASSING {
		t.Errorf("checks %v", pr.Checks)
	}
	// bob's comment-only review keeps his change request; carol is still asked.
	if len(pr.Reviewers) != 2 || pr.Reviewers[0].State != bandav1.ReviewState_REVIEW_STATE_CHANGES_REQUESTED || pr.Reviewers[1].State != bandav1.ReviewState_REVIEW_STATE_REQUESTED {
		t.Errorf("reviewers %v", pr.Reviewers)
	}
	var ids []string
	for _, c := range pr.Comments {
		ids = append(ids, c.Id+":"+c.ReplyTo)
	}
	if strings.Join(ids, " ") != "5: review-1: 10: 11:10" { // oldest first; the empty comment review is left out
		t.Errorf("comments %v", ids)
	}
	if strings.Join(pr.MergeMethods, ",") != "squash,rebase" || pr.Labels[0] != "bug" || pr.Author != "alice" {
		t.Errorf("methods %v, labels %v, author %q", pr.MergeMethods, pr.Labels, pr.Author)
	}
	if err := g.Update(ctx, pr, Edit{Title: ptr("New"), Draft: ptr(true), Reviewers: []string{"erin"}}); err != nil {
		t.Fatal(err)
	}
	if q, _ := f.posted["POST /api/graphql"]["query"].(string); !strings.Contains(q, "convertPullRequestToDraft") || f.posted["PATCH "+gh+"/pulls/8"]["title"] != "New" {
		t.Errorf("update posted %v", f.posted)
	}
	if err := g.Comment(ctx, 8, "top", ""); err != nil {
		t.Fatal(err)
	}
	if err := g.Comment(ctx, 8, "reply", "10"); err != nil || f.posted["POST "+gh+"/pulls/8/comments"]["in_reply_to"] != 10.0 {
		t.Fatalf("reply: %v %v", err, f.posted)
	}
	if err := g.Merge(ctx, 8, "squash"); err != nil || f.posted["PUT "+gh+"/pulls/8/merge"]["merge_method"] != "squash" {
		t.Fatalf("merge: %v %v", err, f.posted)
	}
	var fe *Error
	if err := g.Merge(ctx, 9, "squash"); !errors.As(err, &fe) || fe.Code != Refused || !strings.Contains(fe.Msg, "not mergeable") {
		t.Errorf("merge refused: %v", err)
	}
	if _, err := g.Get(ctx, 404); !errors.As(err, &fe) || fe.Code != NotFound {
		t.Errorf("missing: %v", err)
	}
}

func TestGitHubErrors(t *testing.T) {
	_, base := newFake(t, map[string]any{
		"GET " + gh + "/pulls/1": func(*http.Request) (int, any) {
			return http.StatusUnauthorized, map[string]any{"message": "Bad credentials"}
		},
	})
	var fe *Error
	if _, err := open(t, GitHub, base).Get(context.Background(), 1); !errors.As(err, &fe) || fe.Code != Unauthorized {
		t.Errorf("401: %v", err)
	}
	if _, err := open(t, GitHub, "http://127.0.0.1:1").Get(context.Background(), 1); !errors.As(err, &fe) || fe.Code != Unavailable || strings.Contains(fe.Msg, "127.0.0.1") {
		t.Errorf("unreachable: %v", err)
	}
}

func TestETags(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(`{"number": 3}`))
	}))
	defer srv.Close()
	client := &http.Client{Transport: newETags("etag-test")}
	for i := range 2 {
		res, err := client.Get(srv.URL + "/x")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || string(body) != `{"number": 3}` {
			t.Errorf("call %d: %d %s", i, res.StatusCode, body)
		}
	}
	if calls != 2 {
		t.Errorf("%d calls", calls)
	}
}

const gl = "/api/v4/projects/o%2Fr"

func TestGitLab(t *testing.T) {
	ctx := context.Background()
	f, base := newFake(t, map[string]any{
		"GET " + gl + "/merge_requests":  []any{map[string]any{"iid": 4}},
		"POST " + gl + "/merge_requests": map[string]any{"iid": 5},
		"GET " + gl + "/merge_requests/5": map[string]any{"iid": 5, "title": "Draft: T", "state": "opened", "draft": true, "web_url": "https://gl/mr/5",
			"detailed_merge_status": "not_approved", "author": map[string]any{"username": "alice"}, "sha": "abc", "changes_count": "3",
			"source_branch": "banda/x", "target_branch": "main", "labels": []any{"ui"}, "head_pipeline": map[string]any{"id": 77},
			"reviewers": []any{map[string]any{"id": 1, "username": "bob"}, map[string]any{"id": 2, "username": "carol"}}},
		"GET " + gl + "/pipelines/77/jobs": []any{
			map[string]any{"name": "test", "stage": "test", "status": "running"},
			map[string]any{"name": "build", "stage": "build", "status": "success"}},
		"GET " + gl + "/merge_requests/5/approvals": map[string]any{"approved_by": []any{map[string]any{"user": map[string]any{"username": "bob"}}}},
		"GET " + gl + "/merge_requests/5/discussions": []any{
			map[string]any{"id": "d1", "individual_note": true, "notes": []any{map[string]any{"id": 1, "body": "hi", "author": map[string]any{"username": "dan"}}}},
			map[string]any{"id": "d2", "individual_note": false, "notes": []any{
				map[string]any{"id": 2, "body": "here", "author": map[string]any{"username": "bob"}, "position": map[string]any{"new_path": "a.go", "new_line": 3}},
				map[string]any{"id": 3, "body": "ok", "author": map[string]any{"username": "alice"}},
				map[string]any{"id": 4, "body": "changed the description", "system": true}}}},
		"PUT " + gl + "/merge_requests/5":                       map[string]any{"iid": 5},
		"GET /api/v4/users":                                     []any{map[string]any{"id": 9, "username": "erin"}},
		"POST " + gl + "/merge_requests/5/discussions":          map[string]any{"id": "d3"},
		"POST " + gl + "/merge_requests/5/discussions/d2/notes": map[string]any{"id": 5},
		"PUT " + gl + "/merge_requests/5/merge":                 map[string]any{"iid": 5},
		"PUT " + gl + "/merge_requests/6/merge": func(*http.Request) (int, any) {
			return http.StatusMethodNotAllowed, map[string]any{"message": "405 Method Not Allowed"}
		},
	})
	g := open(t, GitLab, base)
	if n, err := g.Find(ctx, "banda/x"); n != 4 || err != nil {
		t.Fatalf("Find = %d, %v", n, err)
	}
	if n, err := g.Create(ctx, New{Title: "T", Body: "B", Head: "banda/x", Base: "main", Draft: true}); n != 5 || err != nil {
		t.Fatalf("Create = %d, %v", n, err)
	}
	if p := f.posted["POST "+gl+"/merge_requests"]; p["title"] != "Draft: T" || p["source_branch"] != "banda/x" {
		t.Errorf("Create posted %v", p)
	}
	pr, err := g.Get(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	s := pr.Summary
	if s.State != bandav1.PullState_PULL_STATE_DRAFT || s.Mergeable != bandav1.Mergeable_MERGEABLE_BLOCKED || pr.MergeDetail != "Not approved yet" ||
		s.Checks != bandav1.ChecksState_CHECKS_STATE_PENDING || s.Review != bandav1.ReviewState_REVIEW_STATE_APPROVED || pr.ChangedFiles != 3 {
		t.Errorf("summary %v, detail %q", s, pr.MergeDetail)
	}
	if pr.Checks[0].Name != "build / build" || len(pr.Reviewers) != 2 || pr.Reviewers[1].Login != "carol" {
		t.Errorf("checks %v, reviewers %v", pr.Checks, pr.Reviewers)
	}
	if len(pr.Comments) != 3 || pr.Comments[1].Path != "a.go" || pr.Comments[2].ReplyTo != "2" || pr.Comments[0].ReplyTo != "" {
		t.Errorf("comments %v", pr.Comments)
	}
	if err := g.Update(ctx, pr, Edit{Draft: ptr(false), Reviewers: []string{"erin"}}); err != nil {
		t.Fatal(err)
	}
	if p := f.posted["PUT "+gl+"/merge_requests/5"]; p["title"] != "T" || len(p["reviewer_ids"].([]any)) != 3 {
		t.Errorf("update posted %v", p)
	}
	if err := g.Comment(ctx, 5, "reply", "2"); err != nil || f.posted["POST "+gl+"/merge_requests/5/discussions/d2/notes"]["body"] != "reply" {
		t.Fatalf("reply: %v", err)
	}
	if err := g.Comment(ctx, 5, "top", ""); err != nil {
		t.Fatal(err)
	}
	if err := g.Merge(ctx, 5, "squash"); err != nil || f.posted["PUT "+gl+"/merge_requests/5/merge"]["squash"] != true {
		t.Fatalf("merge: %v", err)
	}
	var fe *Error
	if err := g.Merge(ctx, 6, "merge"); !errors.As(err, &fe) || fe.Code != Refused {
		t.Errorf("merge refused: %v", err)
	}
	if f.auth[0] != "tok" {
		t.Errorf("PRIVATE-TOKEN %q", f.auth[0])
	}
}

func ptr[T any](v T) *T { return &v }
