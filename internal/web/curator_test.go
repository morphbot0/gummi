package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/skillpack"
)

// curatorBody is the slice of GET /api/curator a test reads.
type curatorBody struct {
	Config *struct {
		Backend string `json:"backend"`
	} `json:"config"`
	Published string `json:"published"`
	Skills    []struct {
		Name     string `json:"name"`
		Owner    string `json:"owner"`
		Pinned   bool   `json:"pinned"`
		Pinnable bool   `json:"pinnable"`
	} `json:"skills"`
	Runs []struct {
		ID         string `json:"id"`
		Digest     string `json:"digest"`
		Revertable bool   `json:"revertable"`
		Reverted   bool   `json:"reverted"`
	} `json:"runs"`
	Staged []struct {
		Card     string `json:"card"`
		CardName string `json:"cardName"`
		Name     string `json:"name"`
		Op       string `json:"op"`
		Status   string `json:"status"`
		Why      string `json:"why"`
		Verdict  string `json:"verdict"`
	} `json:"staged"`
}

// curatorWrote stands in for a curator run: one skill written to the
// pack in a commit of its own, and the ledger row naming the pack's tip
// before and after it.
func curatorWrote(t *testing.T, b *boardHarness, id, name string) {
	t.Helper()
	ctx := context.Background()
	pack := b.eng.SkillPack()
	if err := pack.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := pack.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	l, err := pack.Write(ctx, skillpack.Draft{
		Name: name, Description: "Run the named tests and check they ran", Body: "Run them, then count them.\n",
	}, nil, "curator "+id+": write "+name)
	if err != nil || !l.OK() {
		t.Fatalf("write %s: %v %v", name, err, l.Refusals)
	}
	after, err := pack.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(engine.CuratorRun{
		ID: id, Why: "schedule", Start: time.Now().Add(-time.Minute), End: time.Now(),
		Backend: "fake", Before: before, After: after, Credits: 3, Outcome: engine.CuratorDone,
		Written: []string{name},
	})
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(b.root, ".gummi", "state", "curator.jsonl")
	f, err := os.OpenFile(ledger, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
}

// TestCuratorRoutes: the read shows a curator's skill and the run that
// wrote it; a person pins and unpins the skill and reverts the run, and
// the read follows each act.
func TestCuratorRoutes(t *testing.T) {
	b := newBoardHarness(t)

	var empty curatorBody
	b.must(http.StatusOK, http.MethodGet, "/api/curator", nil, &empty)
	if empty.Config != nil || len(empty.Skills) != 0 || len(empty.Runs) != 0 || empty.Published != "" {
		t.Fatalf("a board with no curator and no pack reads %+v", empty)
	}

	curatorWrote(t, b, "cur-1", "scoped-tests")
	var got curatorBody
	b.must(http.StatusOK, http.MethodGet, "/api/curator", nil, &got)
	if len(got.Skills) != 1 || got.Skills[0].Name != "scoped-tests" || got.Skills[0].Owner != "curator" || !got.Skills[0].Pinnable {
		t.Fatalf("skills = %+v; want the curator's scoped-tests", got.Skills)
	}
	if len(got.Runs) != 1 || got.Runs[0].ID != "cur-1" || !got.Runs[0].Revertable || !strings.Contains(got.Runs[0].Digest, "wrote scoped-tests") {
		t.Fatalf("runs = %+v; want cur-1, revertable, its digest", got.Runs)
	}

	b.must(http.StatusOK, http.MethodPost, "/api/curator/skills/scoped-tests/pin", struct{}{}, nil)
	b.must(http.StatusOK, http.MethodGet, "/api/curator", nil, &got)
	if s := got.Skills[0]; !s.Pinned || s.Owner != "pinned" {
		t.Fatalf("after pin the skill reads %+v", s)
	}
	b.must(http.StatusOK, http.MethodPost, "/api/curator/skills/scoped-tests/unpin", struct{}{}, nil)
	b.must(http.StatusOK, http.MethodGet, "/api/curator", nil, &got)
	if s := got.Skills[0]; s.Pinned || s.Owner != "curator" {
		t.Fatalf("after unpin the skill reads %+v", s)
	}

	var out map[string]any
	b.must(http.StatusOK, http.MethodPost, "/api/curator/runs/cur-1/revert", struct{}{}, &out)
	if out["ok"] != true {
		t.Fatalf("revert = %v", out)
	}
	b.must(http.StatusOK, http.MethodGet, "/api/curator", nil, &got)
	if len(got.Skills) != 0 {
		t.Fatalf("after the revert the pack still holds %+v", got.Skills)
	}
	if len(got.Runs) != 1 || got.Runs[0].Revertable || !got.Runs[0].Reverted {
		t.Fatalf("after the revert the run reads %+v; want reverted, not revertable", got.Runs)
	}
	if code := b.call(http.MethodPost, "/api/curator/runs/cur-1/revert", struct{}{}, &out); code != http.StatusConflict {
		t.Errorf("a second revert = %d %v, want 409", code, out)
	}
}

// TestCuratorRouteStaged: what card sessions staged reaches the page by
// card title, a pending change with its reason and a rejected one with
// the curator's.
func TestCuratorRouteStaged(t *testing.T) {
	b := newBoardHarness(t)
	ctx := context.Background()
	curatorWrote(t, b, "cur-1", "scoped-tests")
	f := b.card(7, "rate limits", domain.StageImplement)
	pack := b.eng.SkillPack()
	if l, err := pack.StageWrite(ctx, string(f.ID), skillpack.Draft{
		Name: "always-sleep", Description: "Sleep before a flaky assertion", Body: "Sleep a second first.\n",
	}, nil); err != nil || !l.OK() {
		t.Fatalf("stage write: %v %v", err, l.Refusals)
	}
	if err := pack.Reject(string(f.ID), "always-sleep", "a sleep hides the race"); err != nil {
		t.Fatal(err)
	}
	if err := pack.StageArchive(ctx, string(f.ID), "scoped-tests", "the Makefile counts them now"); err != nil {
		t.Fatal(err)
	}

	var got curatorBody
	b.must(http.StatusOK, http.MethodGet, "/api/curator", nil, &got)
	if len(got.Staged) != 2 {
		t.Fatalf("staged = %+v; want the rejected write and the pending archive", got.Staged)
	}
	byName := map[string]int{}
	for i, c := range got.Staged {
		byName[c.Name] = i
		if c.Card != string(f.ID) || c.CardName != f.Name() {
			t.Errorf("staged %s names its card %q %q; want %s %q", c.Name, c.Card, c.CardName, f.ID, f.Name())
		}
	}
	if c := got.Staged[byName["always-sleep"]]; c.Op != "write" || c.Status != "rejected" || c.Verdict != "a sleep hides the race" {
		t.Errorf("the rejected write reads %+v", c)
	}
	if c := got.Staged[byName["scoped-tests"]]; c.Op != "archive" || c.Status != "pending" || c.Why != "the Makefile counts them now" || c.Verdict != "" {
		t.Errorf("the pending archive reads %+v", c)
	}
}

// TestCuratorRouteRefusals: an unknown run or skill is not found, a run
// asked of a board with no curator is a conflict in the engine's words,
// and every write is refused unpaired or cross-origin before the board
// is asked anything.
func TestCuratorRouteRefusals(t *testing.T) {
	b := newBoardHarness(t)
	curatorWrote(t, b, "cur-1", "scoped-tests")

	var out map[string]any
	if code := b.call(http.MethodPost, "/api/curator/runs/nope/revert", struct{}{}, &out); code != http.StatusNotFound {
		t.Errorf("revert of an unknown run = %d %v, want 404", code, out)
	}
	if code := b.call(http.MethodPost, "/api/curator/skills/nope/pin", struct{}{}, &out); code != http.StatusNotFound {
		t.Errorf("pin of an unknown skill = %d %v, want 404", code, out)
	}
	code := b.call(http.MethodPost, "/api/curator/run", struct{}{}, &out)
	if code != http.StatusConflict || !strings.Contains(strings.ToLower(out["error"].(string)), "no curator is configured") {
		t.Errorf("run with no curator = %d %v, want 409 naming the missing config", code, out)
	}

	unpaired := b.client()
	if code := postStatus(t, b, unpaired, "/api/curator/runs/cur-1/revert", `{}`, b.http.URL); code != http.StatusUnauthorized {
		t.Errorf("unpaired revert = %d, want 401", code)
	}
	paired := b.client()
	b.pair(paired, "Cross")
	for _, path := range []string{"/api/curator/run", "/api/curator/runs/cur-1/revert", "/api/curator/skills/scoped-tests/pin", "/api/curator/skills/scoped-tests/unpin"} {
		if code := postStatus(t, b, paired, path, `{}`, "https://evil.example"); code != http.StatusForbidden {
			t.Errorf("cross-origin %s = %d, want 403", path, code)
		}
	}
	var got curatorBody
	b.must(http.StatusOK, http.MethodGet, "/api/curator", nil, &got)
	if len(got.Skills) != 1 || got.Skills[0].Pinned {
		t.Fatalf("a refused write changed the pack: %+v", got.Skills)
	}
}

// TestCuratorConfigRoute: a person turns the curator on from the page,
// the block lands in the workspace config and the board runs on it at
// once; a block it could not curate on is refused before anything is
// written, and turning it off removes the block again.
func TestCuratorConfigRoute(t *testing.T) {
	b := newBoardHarness(t)
	cfgPath := filepath.Join(b.root, ".gummi", "config.yaml")

	var out map[string]any
	for _, bad := range []map[string]any{
		{"backend": "", "budget": 100},
		{"backend": "nope", "budget": 100},
		{"backend": "claude", "budget": 0},
		{"backend": "claude", "budget": 100, "monthly": 50},
		{"backend": "claude", "budget": 100, "schedule": "not a schedule"},
	} {
		if code := b.call(http.MethodPut, "/api/curator/config", bad, &out); code != http.StatusBadRequest {
			t.Errorf("PUT %v = %d %v, want 400", bad, code, out)
		}
	}
	if raw, _ := os.ReadFile(cfgPath); strings.Contains(string(raw), "curator") {
		t.Fatalf("a refused block was written:\n%s", raw)
	}

	var got struct {
		Config *struct {
			Backend  string  `json:"backend"`
			Model    string  `json:"model"`
			Schedule string  `json:"schedule"`
			Budget   float64 `json:"budget"`
			Monthly  float64 `json:"monthly"`
		} `json:"config"`
		Next time.Time `json:"next"`
	}
	b.must(http.StatusOK, http.MethodPut, "/api/curator/config",
		map[string]any{"backend": " claude ", "model": "haiku", "schedule": "@weekly", "budget": 100, "monthly": 1000}, &got)
	if c := got.Config; c == nil || c.Backend != "claude" || c.Model != "haiku" || c.Schedule != "@weekly" || c.Budget != 100 || c.Monthly != 1000 || got.Next.IsZero() {
		t.Fatalf("after PUT the read is %+v (next %v)", c, got.Next)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(raw), "curator:") || !strings.Contains(string(raw), "backend: claude") {
		t.Fatalf("config.yaml after PUT:\n%s (%v)", raw, err)
	}
	if c := b.eng.CuratorConfig(); c == nil || c.Backend != "claude" {
		t.Fatalf("the engine runs on %+v, want the block just saved", c)
	}
	// the board takes a run at once: the refusal a curator-less board gives is gone
	b.must(http.StatusOK, http.MethodPost, "/api/curator/run", struct{}{}, &out)

	got.Config = nil // the answer omits it when off
	b.must(http.StatusOK, http.MethodDelete, "/api/curator/config", nil, &got)
	if got.Config != nil || b.eng.CuratorConfig() != nil {
		t.Fatalf("after DELETE the curator is still %+v / %+v", got.Config, b.eng.CuratorConfig())
	}
	if raw, _ := os.ReadFile(cfgPath); strings.Contains(string(raw), "curator") {
		t.Fatalf("config.yaml still holds the curator:\n%s", raw)
	}

	paired := b.client()
	b.pair(paired, "Cross")
	req, _ := http.NewRequest(http.MethodPut, b.http.URL+"/api/curator/config", strings.NewReader(`{"backend":"claude","budget":100}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := paired.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || b.eng.CuratorConfig() != nil {
		t.Errorf("cross-origin PUT = %d, curator %+v; want 403 and no curator", resp.StatusCode, b.eng.CuratorConfig())
	}
}
