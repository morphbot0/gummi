package ui

// A session's spec on the web face (DESIGN §24.2, §25.3): the one spec
// the reader is told (UseSpec, spec 1 by default), read with the rest of
// a card's documents (WebDocs.Spec), started from the template, edited
// whole, and every write a numbered revision — the engine's
// StartSessionSpec and WriteSessionSpec, which a running crew hears of as
// a steer. Notes and resolutions are written the way a workflow card's
// are, and recorded as a revision after.

import (
	"context"
	"os"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/threadfold"
	"github.com/morphis/gummi/internal/webapi"
)

// hasSessionSpec says whether a card may keep a session spec: a session
// that is not a crew's own card or node, nor an agent in a home of its own.
func hasSessionSpec(f domain.Feature) bool {
	return f.IsFreeform() && !f.InCrew() && !f.Home
}

// UseSpec points the reader at the session's spec n (DESIGN §25.3); a
// reader not told reads spec 1.
func (d *WebDocs) UseSpec(n int) {
	if n > 0 {
		d.specN = n
	}
}

// sessionSpecN is the session spec this reader acts on.
func (d *WebDocs) sessionSpecN() int {
	if d.specN > 0 {
		return d.specN
	}
	return 1
}

// sessionSpecPath is that spec's file, "" when the session has no such
// spec.
func (d *WebDocs) sessionSpecPath() string {
	path, _ := d.store.SessionSpecFile(context.Background(), d.pool.Root(), d.f.ID, d.sessionSpecN())
	return path
}

// specRevisionBy is who a revision is by, in the page's words.
func specRevisionBy(by string) string {
	switch by {
	case engine.ActorAgent:
		return "agent"
	case state.ActorCrew:
		return "crew"
	}
	if n := threadfold.TurnBy(by); n != "" {
		return n
	}
	return "you"
}

// sessionSpec fills a session spec's revisions and done-when list.
func (d *WebDocs) sessionSpec(ctx context.Context, content string, out *webapi.Spec) {
	out.Session = true
	frozen := map[int][]int{}
	// a crew froze a revision of spec 1, the one spec it knew
	if crews, err := d.store.SessionCrews(ctx, d.f.ID); err == nil && d.sessionSpecN() == 1 {
		for _, c := range crews {
			if c.SpecRev > 0 {
				frozen[c.SpecRev] = append(frozen[c.SpecRev], c.Seq)
			}
		}
	}
	if revs, err := d.store.SpecRevisions(ctx, d.f.ID, d.sessionSpecN()); err == nil {
		for _, r := range revs {
			out.Revisions = append(out.Revisions, webapi.SpecRevision{Rev: r.Rev, By: specRevisionBy(r.By), At: r.At, Crews: frozen[r.Rev]})
		}
	}
	if items, _, err := spec.ParseDoneWhen(content); err == nil {
		out.DoneWhen = webCrewItems(items)
	}
}

// noteSessionRevision records a session spec's current text as a
// revision after a note or a resolution was written into it.
func (d *WebDocs) noteSessionRevision(ctx context.Context, path, person string) error {
	if !d.f.IsFreeform() || d.eng == nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = d.eng.WriteSessionSpec(ctx, d.fresh(ctx), d.sessionSpecN(), string(raw), state.PersonActor(person))
	return crewRefusal(err)
}

// StartSessionSpec is POST /api/cards/{id}/spec/start: a new spec on the
// session from the template, as its first revision. The answer is the new
// spec.
func (d *WebDocs) StartSessionSpec(ctx context.Context, title, person string) (webapi.Spec, error) {
	if !hasSessionSpec(d.f) {
		return webapi.Spec{}, ErrNoCard
	}
	if d.eng == nil {
		return webapi.Spec{}, ErrDetached
	}
	if err := closedToReview(d.f, "it takes no new spec"); err != nil {
		return webapi.Spec{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = d.f.Title
	}
	sp, _, err := d.eng.StartSessionSpec(ctx, d.fresh(ctx), title, state.PersonActor(person))
	if err != nil {
		return webapi.Spec{}, crewRefusal(err)
	}
	d.UseSpec(sp.N)
	return d.Spec(ctx)
}

// WriteSessionSpec is PUT /api/cards/{id}/spec: the person's edit of the
// whole spec, as one new revision. rev must be the Spec.Rev the edit was
// made from, or the spec moved under the editor (ErrMoved).
func (d *WebDocs) WriteSessionSpec(ctx context.Context, req webapi.SpecWriteRequest, person string) (webapi.Spec, error) {
	if !hasSessionSpec(d.f) {
		return webapi.Spec{}, ErrNoCard
	}
	if d.eng == nil {
		return webapi.Spec{}, ErrDetached
	}
	if err := closedToReview(d.f, "its spec takes no more edits"); err != nil {
		return webapi.Spec{}, err
	}
	path := d.artifact()
	if path == "" {
		return webapi.Spec{}, invalid("this session has no spec yet: start one first")
	}
	if strings.TrimSpace(req.Markdown) == "" {
		return webapi.Spec{}, invalid("a spec cannot be empty")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return webapi.Spec{}, err
	}
	if req.Rev != spec.Rev(raw) {
		return webapi.Spec{}, ErrMoved
	}
	content := strings.ReplaceAll(req.Markdown, "\r\n", "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if content != string(raw) {
		if _, err := d.eng.WriteSessionSpec(ctx, d.fresh(ctx), d.sessionSpecN(), content, state.PersonActor(person)); err != nil {
			return webapi.Spec{}, crewRefusal(err)
		}
	}
	return d.Spec(ctx)
}
