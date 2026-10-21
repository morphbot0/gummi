package engine

// A session's own spec (DESIGN §24.2). A session reaches it through the
// same spec_view, spec_replace_section and spec_annotate a stage's
// architect uses, plus spec_start, which creates it. It lives with the
// workspace (Feature.SpecPath), never on the branch, and every write is a
// numbered revision in the store: a crew freezes one.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/atomicfile"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
)

const specStartToolName = "spec_start"

func specStartTool() agent.ToolDef {
	return agent.ToolDef{
		Name: specStartToolName,
		Description: "Create this session's spec from gummi's template: Problem, Approach, Done when " +
			"(a gummi-done-when block), Limits and Notes. A session has at most one spec; it is " +
			"optional, and a crew needs one with at least one done-when item. Fill it with " +
			"spec_replace_section.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{"type": "string", "description": "The spec's title."},
			},
		},
	}
}

// sessionSpecTools are a session's spec tools, offered to every session
// that is not a crew's.
func sessionSpecTools() []agent.ToolDef {
	return []agent.ToolDef{specStartTool(), specViewTool(), specReplaceSectionTool(), specAnnotateTool()}
}

// SessionSpecPath is where a session's spec lives.
func (e *Engine) SessionSpecPath(f domain.Feature) string {
	return filepath.Join(e.pool.Root(), f.SpecPath())
}

// SessionSpec reads a session's spec; ok is false when it has none.
func (e *Engine) SessionSpec(f domain.Feature) (string, bool) {
	raw, err := os.ReadFile(e.SessionSpecPath(f))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// handleSpecStart creates the session's spec.
func (e *Engine) handleSpecStart(s *Session, tc *agent.ToolCall) {
	var a struct {
		Title string `json:"title"`
	}
	_ = json.Unmarshal(tc.Args, &a)
	f := s.Feature
	if !f.IsFreeform() || f.InCrew() {
		e.resolveNow(s, tc.ID, "spec_start is a session's: this card has no spec of its own")
		return
	}
	title := strings.TrimSpace(a.Title)
	if title == "" {
		title = f.Title
	}
	_, rev, err := e.StartSessionSpec(context.Background(), f, title, ActorAgent)
	if err != nil {
		e.resolveNow(s, tc.ID, err.Error())
		return
	}
	s.setSpecPath(e.SessionSpecPath(f))
	s.appendActivity("started the spec")
	e.persist(s)
	e.resolveNow(s, tc.ID, fmt.Sprintf("created the spec (revision %d); fill its sections with spec_replace_section", rev))
}

// ActorAgent is who a spec revision the session's agent wrote is by.
const ActorAgent = "agent"

// errSpecExists refuses a second spec.
var errSpecExists = errors.New("this session already has a spec; read it with spec_view")

// StartSessionSpec creates a session's spec and its first revision.
func (e *Engine) StartSessionSpec(ctx context.Context, f domain.Feature, title, by string) (string, int, error) {
	path := e.SessionSpecPath(f)
	if _, err := os.Stat(path); err == nil {
		return "", 0, errSpecExists
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", 0, err
	}
	body := spec.SessionTemplate(title)
	if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
		return "", 0, err
	}
	rev, err := e.recordSpecRevision(ctx, f.ID, body, by)
	return body, rev, err
}

// WriteSessionSpec replaces a session's spec with content a person wrote
// on the Spec tab, as one new revision.
func (e *Engine) WriteSessionSpec(ctx context.Context, f domain.Feature, content, by string) (int, error) {
	if !f.IsFreeform() || f.InCrew() {
		return 0, fmt.Errorf("%s has no spec of its own", f.ID)
	}
	path := e.SessionSpecPath(f)
	if _, err := os.Stat(path); err != nil {
		return 0, fmt.Errorf("this session has no spec yet")
	}
	unlock := spec.LockFile(path)
	defer unlock()
	if err := atomicfile.Write(path, []byte(content), 0o600); err != nil {
		return 0, err
	}
	rev, err := e.recordSpecRevision(ctx, f.ID, content, by)
	if err == nil {
		e.crewSpecEdited(ctx, f, rev)
	}
	return rev, err
}

// recordSpecRevision saves the spec's current text as a revision.
func (e *Engine) recordSpecRevision(ctx context.Context, id domain.FeatureID, content, by string) (int, error) {
	if e.cfg.Store == nil {
		return 0, nil
	}
	rev, err := e.cfg.Store.AddSpecRevision(ctx, id, content, by, e.now())
	if err == nil {
		e.send(Event{Feature: id, Stage: domain.StageOpen, Kind: EventUpdated})
	}
	return rev, err
}

// noteSessionSpecWrite records a revision after the agent wrote the
// session's spec through a stage tool, and tells a running crew.
func (e *Engine) noteSessionSpecWrite(s *Session) {
	f := s.Feature
	if !f.IsFreeform() || f.InCrew() {
		return
	}
	raw, err := os.ReadFile(s.SpecPath())
	if err != nil {
		return
	}
	ctx := context.Background()
	if rev, err := e.recordSpecRevision(ctx, f.ID, string(raw), ActorAgent); err == nil {
		e.crewSpecEdited(ctx, f, rev)
	}
}

const sessionCrewHint = `This session may keep a spec and call on a crew (gummi's DESIGN §24). spec_start creates the session's spec (Problem, Approach, Done when, Limits, Notes); spec_view and spec_replace_section read and write it; the person edits it too. When the work is worth splitting — several parts that divide cleanly — and the spec has done-when items with checks, crew_start asks for a crew: a planner plans nodes, the person approves, and specialists build, each checked and reviewed, on the crew's own branch. It returns at once; the report arrives later as a turn from the crew, and crew_land lands the crew's branch here. Small changes need no crew: do them yourself.`

func writeFile(path, content string) error { return atomicfile.Write(path, []byte(content), 0o600) }
