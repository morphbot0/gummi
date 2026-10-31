package engine

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
)

// specCall runs one of a session's spec tools and returns what it
// resolved with.
func specCall(t *testing.T, e *Engine, s *Session, id, name, args string) string {
	t.Helper()
	e.handleClientTool(s, &agent.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)})
	r, ok := s.agent().(toolResolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	got, done := r.Resolved(id)
	if !done {
		t.Fatalf("%s %s never resolved", name, args)
	}
	return got
}

func specToolsSession(t *testing.T) (*Engine, *Session, domain.Feature) {
	t.Helper()
	ag := agent.NewFake("")
	ag.Caps = agent.Capabilities{ClientTools: true, Interrupt: true}
	e := newEngine(t, ag)
	f := freeformCard(1, "Two pieces")
	s := attach(t, e, f)
	waitIdle(t, e, f.ID)
	return e, s, f
}

// A session keeps a list of specs (DESIGN §25.3): spec_start adds one and
// says its number, a spec tool may leave spec out while there is one, and
// must name it once there are two — so no write lands in the wrong spec.
func TestASessionKeepsAListOfSpecs(t *testing.T) {
	e, s, f := specToolsSession(t)
	ctx := context.Background()

	if got := specCall(t, e, s, "v0", specViewToolName, `{}`); got != errNoSessionSpec.Error() {
		t.Fatalf("spec_view with no spec = %q", got)
	}
	if got := specCall(t, e, s, "s1", specStartToolName, `{"title":"Export as JSON"}`); !strings.Contains(got, "created spec 1") {
		t.Fatalf("first spec_start = %q", got)
	}
	// one spec: spec may be left out
	if got := specCall(t, e, s, "r1", specReplaceSectionToolName, `{"section":"Problem","body":"json export\n"}`); got != "updated Problem section" {
		t.Fatalf("replace on the only spec = %q", got)
	}
	if got := specCall(t, e, s, "s2", specStartToolName, `{"title":"Import CSV"}`); !strings.Contains(got, "created spec 2") {
		t.Fatalf("second spec_start = %q", got)
	}
	specs, err := e.SessionSpecs(ctx, f)
	if err != nil || len(specs) != 2 || specs[1].Title != "Import CSV" || specs[1].State != domain.SpecDraft {
		t.Fatalf("specs = %+v %v", specs, err)
	}
	if want := domain.SessionSpecPath(f.ID, 2, "Import CSV"); specs[1].Path != want {
		t.Errorf("spec 2's path = %q, want %q", specs[1].Path, want)
	}

	// two specs: leaving spec out is refused, and changes nothing
	before, _ := e.SessionSpec(f, 2)
	for _, call := range []struct{ name, args string }{
		{specViewToolName, `{"section":"Problem"}`},
		{specReplaceSectionToolName, `{"section":"Problem","body":"csv\n"}`},
		{annotateToolName, `{"anchor":"Problem","note":"which?"}`},
	} {
		if got := specCall(t, e, s, "x-"+call.name, call.name, call.args); !strings.Contains(got, "name the one to act on with spec") {
			t.Errorf("%s without spec on two specs = %q", call.name, got)
		}
	}
	if after, _ := e.SessionSpec(f, 2); after != before {
		t.Error("a refused write changed spec 2")
	}

	// named, each acts on its own spec
	if got := specCall(t, e, s, "r2", specReplaceSectionToolName, `{"spec":2,"section":"Problem","body":"csv import\n"}`); got != "updated spec 2's Problem section" {
		t.Fatalf("replace on spec 2 = %q", got)
	}
	if got := specCall(t, e, s, "v1", specViewToolName, `{"spec":"S1","section":"Problem"}`); !strings.Contains(got, "json export") {
		t.Errorf("spec_view spec 1 = %q", got)
	}
	if got := specCall(t, e, s, "v2", specViewToolName, `{"spec":2,"section":"Problem"}`); !strings.Contains(got, "csv import") {
		t.Errorf("spec_view spec 2 = %q", got)
	}
	if got := specCall(t, e, s, "v3", specViewToolName, `{"spec":3}`); !strings.Contains(got, "no spec 3") {
		t.Errorf("spec_view spec 3 = %q", got)
	}
	if got := specCall(t, e, s, "a2", annotateToolName, `{"spec":2,"anchor":"csv import","note":"which delimiter?"}`); got != "annotation added to the spec" {
		t.Errorf("annotate spec 2 = %q", got)
	}
	if text, _ := e.SessionSpec(f, 2); !strings.Contains(text, "which delimiter?") {
		t.Errorf("spec 2 lacks the annotation:\n%s", text)
	}
	if text, _ := e.SessionSpec(f, 1); strings.Contains(text, "which delimiter?") || strings.Contains(text, "csv") {
		t.Errorf("spec 1 took spec 2's writes:\n%s", text)
	}

	// every write is a revision of its own spec
	revs1, _ := e.cfg.Store.SpecRevisions(ctx, f.ID, 1)
	revs2, _ := e.cfg.Store.SpecRevisions(ctx, f.ID, 2)
	if len(revs1) != 2 || len(revs2) != 3 {
		t.Fatalf("revisions: spec 1 has %d, spec 2 has %d; want 2 and 3", len(revs1), len(revs2))
	}
	last, ok, _ := e.cfg.Store.SpecRevision(ctx, f.ID, 2, revs2[0].Rev)
	if text, _ := e.SessionSpec(f, 2); !ok || last.Content != text || last.By != ActorAgent {
		t.Errorf("spec 2's last revision is not its text: %+v", last)
	}

	list := specCall(t, e, s, "l1", specListToolName, `{}`)
	for _, want := range []string{`S1 "Export as JSON" — draft, revision 2`, `S2 "Import CSV" — draft, revision 3`} {
		if !strings.Contains(list, want) {
			t.Errorf("spec_list lacks %q:\n%s", want, list)
		}
	}
}

// A person's write through the faces goes to the spec it names, as that
// spec's next revision.
func TestWriteSessionSpecTakesASpecNumber(t *testing.T) {
	e, _, f := specToolsSession(t)
	ctx := context.Background()
	for _, title := range []string{"one", "two"} {
		if _, _, err := e.StartSessionSpec(ctx, f, title, "Simon"); err != nil {
			t.Fatal(err)
		}
	}
	body := spec.SessionTemplate("two, rewritten")
	rev, err := e.WriteSessionSpec(ctx, f, 2, body, "Simon")
	if err != nil || rev != 2 {
		t.Fatalf("write spec 2: rev %d %v", rev, err)
	}
	if raw, _ := os.ReadFile(e.SessionSpecPath(f, 2)); string(raw) != body {
		t.Errorf("spec 2 on disk = %q", raw)
	}
	if text, _ := e.SessionSpec(f, 1); strings.Contains(text, "rewritten") {
		t.Error("spec 1 took spec 2's write")
	}
	if _, err := e.WriteSessionSpec(ctx, f, 3, body, "Simon"); err == nil {
		t.Error("a write to a spec the session lacks was taken")
	}
}

// The spec tools a session is offered take the optional spec, and
// spec_list is among them.
func TestSessionSpecToolsTakeASpec(t *testing.T) {
	names := map[string]agent.ToolDef{}
	for _, d := range sessionSpecTools() {
		names[d.Name] = d
	}
	for _, n := range []string{specViewToolName, specReplaceSectionToolName, annotateToolName} {
		props, _ := names[n].Parameters["properties"].(map[string]any)
		if _, ok := props["spec"]; !ok {
			t.Errorf("%s has no spec argument", n)
		}
	}
	if _, ok := names[specListToolName]; !ok {
		t.Error("spec_list is not offered")
	}
	// the stage tool's own definition is left as it was
	if props, _ := specViewTool().Parameters["properties"].(map[string]any); props["spec"] != nil {
		t.Error("withSpecArg changed the shared definition")
	}
}

// A section's text under another name — claude has sent "content" — is
// refused and writes nothing, rather than emptying the section and
// reporting it updated; a call with no body is refused too, while an empty
// body still clears the section.
func TestReplaceSectionRefusesABodyUnderAnotherName(t *testing.T) {
	e, s, _ := specToolsSession(t)
	if got := specCall(t, e, s, "s1", specStartToolName, `{"title":"Export as JSON"}`); !strings.Contains(got, "created spec 1") {
		t.Fatalf("spec_start = %q", got)
	}
	if got := specCall(t, e, s, "r1", specReplaceSectionToolName, `{"section":"Problem","body":"json export\n"}`); got != "updated Problem section" {
		t.Fatalf("replace = %q", got)
	}
	problem := func() string {
		t.Helper()
		doc := specCall(t, e, s, "v", specViewToolName, `{}`)
		_, after, ok := strings.Cut(doc, "## Problem\n")
		if !ok {
			t.Fatalf("spec_view = %q", doc)
		}
		body, _, _ := strings.Cut(after, "## ")
		return strings.TrimSpace(body)
	}
	for _, args := range []string{
		`{"spec":1,"section":"Problem","content":"lost\n"}`,
		`{"section":"Problem","body":"lost\n","text":"lost\n"}`,
		`{"section":"Problem"}`,
	} {
		got := specCall(t, e, s, "bad", specReplaceSectionToolName, args)
		if !strings.Contains(got, "Nothing was written") || !strings.Contains(got, "body") {
			t.Errorf("%s = %q, want a refusal naming body", args, got)
		}
		if p := problem(); p != "json export" {
			t.Errorf("after %s the Problem section reads %q", args, p)
		}
	}
	if got := specCall(t, e, s, "r2", specReplaceSectionToolName, `{"section":"Problem","body":""}`); got != "updated Problem section" {
		t.Fatalf("an empty body = %q", got)
	}
	if p := problem(); p != "" {
		t.Errorf("an empty body left %q", p)
	}
}

// TestReplaceSectionRefusesADoneWhenItCannotRead: Done when items written
// as a plain list would be read as none, so the write is refused with the
// block's shape and nothing is written; the block form is taken.
func TestReplaceSectionRefusesADoneWhenItCannotRead(t *testing.T) {
	e, s, _ := specToolsSession(t)
	if got := specCall(t, e, s, "s1", specStartToolName, `{"title":"Export as JSON"}`); !strings.Contains(got, "created spec 1") {
		t.Fatalf("spec_start = %q", got)
	}
	before := specCall(t, e, s, "v1", specViewToolName, `{}`)
	got := specCall(t, e, s, "r1", specReplaceSectionToolName, `{"section":"Done when","body":"- DW-1: export works. check: `+"`go test ./...`"+`\n"}`)
	if !strings.Contains(got, "```gummi-done-when") || !strings.Contains(got, "Nothing was written") {
		t.Fatalf("a plain-list Done when = %q", got)
	}
	if after := specCall(t, e, s, "v2", specViewToolName, `{}`); after != before {
		t.Errorf("the refused write changed the spec:\n%s", after)
	}
	block := `{"section":"Done when","body":"` + "```gummi-done-when\\n- id: DW-1\\n  says: export works\\n  check: go test ./...\\n```\\n" + `"}`
	if got := specCall(t, e, s, "r2", specReplaceSectionToolName, block); got != "updated Done when section" {
		t.Fatalf("the block form = %q", got)
	}
}
