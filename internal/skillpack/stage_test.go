package skillpack

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A staged write reaches only the staging card's overlay, and the pack is
// untouched until the change is taken in.
func TestStagedChangesReachOnlyTheirCard(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	p.StageDir = t.TempDir()
	if l, err := p.Write(ctx, draft("go-tests", ""), nil, "curator x: write go-tests"); err != nil || !l.OK() {
		t.Fatalf("write: %v %s", err, l)
	}
	if l, err := p.Write(ctx, draft("old-trick", ""), nil, "curator x: write old-trick"); err != nil || !l.OK() {
		t.Fatalf("write: %v %s", err, l)
	}
	rev := p.Published(ctx)
	base, err := p.Materialize(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}

	d := draft("go-tests", "")
	d.Body = "Run `make ci` before calling a change done."
	if l, err := p.StageWrite(ctx, "FF-1", d, nil); err != nil || !l.OK() {
		t.Fatalf("stage write: %v %s", err, l)
	}
	if l, err := p.StageWrite(ctx, "FF-1", draft("new-one", ""), nil); err != nil || !l.OK() {
		t.Fatalf("stage new: %v %s", err, l)
	}
	if err := p.StageArchive(ctx, "FF-1", "old-trick", "the trick is gone"); err != nil {
		t.Fatal(err)
	}
	if p.Published(ctx) != rev {
		t.Fatal("staging moved published")
	}

	view, err := p.Overlay(base, "FF-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(DirsFor(view, "")); got != "go-tests,new-one" {
		t.Errorf("FF-1 sees %q, want its write and new skill and not the archived one", got)
	}
	md, _ := os.ReadFile(filepath.Join(view, "go-tests", "SKILL.md"))
	if _, body, _ := Parse(md); body != "Run `make ci` before calling a change done.\n" {
		t.Errorf("FF-1 reads %q, want its staged body", body)
	}
	other, err := p.Overlay(base, "FF-2")
	if err != nil || other != base {
		t.Errorf("another card reads %q (%v), want the published revision itself", other, err)
	}
	if again, _ := p.Overlay(base, "FF-1"); again != view {
		t.Errorf("an unchanged stage composed a second view: %s, %s", view, again)
	}

	all := p.AllStaged()
	if len(all["FF-1"]) != 3 {
		t.Fatalf("pending = %+v", all)
	}

	// The curator rejects one and takes one in: the rejected one stays,
	// with its reason, and reaches no session; the taken one is gone.
	if err := p.Reject("FF-1", "new-one", "a one-off"); err != nil {
		t.Fatal(err)
	}
	staged, err := p.StagedDraft("FF-1", "go-tests")
	if err != nil || staged.Body == "" {
		t.Fatalf("staged draft: %+v %v", staged, err)
	}
	if l, err := p.Write(ctx, staged, nil, "curator y: accept"); err != nil || !l.OK() {
		t.Fatalf("accept: %v %s", err, l)
	}
	if err := p.Unstage("FF-1", "go-tests"); err != nil {
		t.Fatal(err)
	}
	if err := p.Unstage("FF-1", "old-trick"); err != nil {
		t.Fatal(err)
	}
	list := p.StagedFor("FF-1")
	if len(list) != 1 || list[0].Name != "new-one" || list[0].Status != StatusRejected || list[0].Verdict != "a one-off" {
		t.Errorf("after review FF-1 holds %+v", list)
	}
	if view, _ := p.Overlay(base, "FF-1"); view != base {
		t.Errorf("a rejected change still reaches the card: %s", view)
	}

	// A released card's views go; a rejected change it holds stays.
	p.ReleaseStage(func(string) bool { return false })
	if len(p.StagedFor("FF-1")) != 1 {
		t.Error("release dropped a rejected change the card can still read")
	}
	if _, err := os.Stat(filepath.Join(p.StageDir, "FF-1", "views")); !os.IsNotExist(err) {
		t.Errorf("release kept the card's views: %v", err)
	}
}

// A session is held to the same lint as the curator and cannot touch a
// person's skill.
func TestStagingRefusesWhatTheCuratorCouldNot(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	p.StageDir = t.TempDir()
	if l, err := p.StageWrite(ctx, "FF-1", Draft{Name: "Bad Name", Description: "x", Body: "y"}, nil); err != nil || l.OK() {
		t.Errorf("a bad name was staged: %v %s", err, l)
	}
	if err := p.StageArchive(ctx, "FF-1", "nothing", "why"); err == nil {
		t.Error("archiving a skill the pack lacks was staged")
	}
	if _, err := p.StageWrite(ctx, "../x", draft("ok", ""), nil); err == nil {
		t.Error("a card id with a path was accepted")
	}
	if len(p.StagedFor("FF-1")) != 0 {
		t.Errorf("a refusal staged %+v", p.StagedFor("FF-1"))
	}
}
