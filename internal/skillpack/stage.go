package skillpack

// stage.go is a card's staged changes to the pack (DESIGN §23.5): what a
// card's own sessions wrote or archived through gummi's skill tools, kept
// out of the pack until the curator reviews it. Only the card's own
// sessions read it, layered over the revision they pinned (Overlay); no
// other card sees it, so a bad lesson cannot spread before review.
//
// Plain files, not commits: <StageDir>/<card>/stage.json lists the
// changes, skills/<name>/ holds each staged write, and views/ holds the
// composed packs the card's sessions read. The curator's verdict turns an
// accepted change into a pack commit; a rejected one stays, with its
// reason, for the card to read, and reaches no session.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/atomicfile"
)

// A staged change's operation and status.
const (
	OpWrite   = "write"
	OpArchive = "archive"

	StatusPending  = "pending"
	StatusRejected = "rejected"
)

// Staged is one change a card staged.
type Staged struct {
	Name string `json:"name"`
	Op   string `json:"op"`
	// Base is the published revision when it was staged: the curator
	// re-reads a skill that has moved since.
	Base   string    `json:"base,omitempty"`
	At     time.Time `json:"at"`
	Why    string    `json:"why,omitempty"` // an archive's reason
	Status string    `json:"status"`
	// Verdict is the curator's reason for a rejection.
	Verdict string `json:"verdict,omitempty"`
}

// Pending reports whether the change still waits for review and reaches
// the card's sessions.
func (s Staged) Pending() bool { return s.Status == StatusPending }

func (p *Pack) stageDir(card string) (string, error) {
	if card == "" || strings.ContainsAny(card, `/\.`) || p.StageDir == "" {
		return "", fmt.Errorf("no card %q to stage for", card)
	}
	return filepath.Join(p.StageDir, card), nil
}

func (p *Pack) readStage(dir string) []Staged {
	var list []Staged
	if raw, err := os.ReadFile(filepath.Join(dir, "stage.json")); err == nil {
		_ = json.Unmarshal(raw, &list)
	}
	return list
}

func (p *Pack) writeStage(dir string, list []Staged) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if len(list) == 0 {
		_ = os.Remove(filepath.Join(dir, "stage.json"))
		return nil
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, "stage.json"), raw, 0o600)
}

// upsert puts c in list in place of any change to the same skill.
func upsert(list []Staged, c Staged) []Staged {
	list = slices.DeleteFunc(list, func(s Staged) bool { return s.Name == c.Name })
	list = append(list, c)
	slices.SortFunc(list, func(a, b Staged) int { return strings.Compare(a.Name, b.Name) })
	return list
}

// StageWrite lints d as the curator's own write would be linted, against
// the published pack, and if it passes stages it for card: a new skill, or
// a whole replacement of a curator skill. A refused draft stages nothing.
func (p *Pack) StageWrite(ctx context.Context, card string, d Draft, repos []string) (Lint, error) {
	dir, err := p.stageDir(card)
	if err != nil {
		return Lint{}, err
	}
	if len(d.Replaces) > 0 {
		return Lint{Refusals: []string{"a session stages one skill at a time; archive the others with their own change"}}, nil
	}
	if err := p.Ensure(ctx); err != nil {
		return Lint{}, err
	}
	rev := p.Published(ctx)
	existing := map[string]Meta{}
	if rev != "" {
		if existing, err = p.Skills(ctx, rev); err != nil {
			return Lint{}, err
		}
	}
	l := LintDraft(d, existing, repos)
	if !l.OK() {
		return l, nil
	}
	if p.Missing != nil && d.Scope != "" && d.Scope != ScopeGeneral {
		if missing := p.Missing(ctx, d.Scope, RepoPaths(d.Body)); len(missing) > 0 {
			l.Warnings = append(l.Warnings, fmt.Sprintf("the %s repository's base branch has no %s; check the path is right", d.Scope, strings.Join(missing, ", ")))
		}
	}
	defer p.lock()()
	skill := filepath.Join(dir, "skills", d.Name)
	if err := os.RemoveAll(skill); err != nil {
		return l, err
	}
	scope := d.Scope
	if scope == "" {
		scope = ScopeGeneral
	}
	files := map[string][]byte{"SKILL.md": Render(Meta{Name: d.Name, Description: strings.TrimSpace(d.Description), Scope: scope, Source: SourceCurator}, d.Body)}
	for rel, c := range d.Files {
		files[rel] = []byte(c)
	}
	for rel, c := range files {
		path := filepath.Join(skill, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return l, err
		}
		if err := os.WriteFile(path, c, 0o644); err != nil { //nolint:gosec // read by the card's sessions
			return l, err
		}
	}
	list := upsert(p.readStage(dir), Staged{Name: d.Name, Op: OpWrite, Base: rev, At: time.Now(), Status: StatusPending})
	return l, p.writeStage(dir, list)
}

// StageArchive stages archiving a published curator skill for card.
func (p *Pack) StageArchive(ctx context.Context, card, name, why string) error {
	dir, err := p.stageDir(card)
	if err != nil {
		return err
	}
	rev := p.Published(ctx)
	if rev == "" {
		return fmt.Errorf("no skill %q to archive", name)
	}
	existing, err := p.Skills(ctx, rev)
	if err != nil {
		return err
	}
	if err := archivable(existing, name); err != nil {
		return err
	}
	if strings.TrimSpace(why) == "" {
		return errors.New("say why the skill should go")
	}
	defer p.lock()()
	_ = os.RemoveAll(filepath.Join(dir, "skills", name))
	list := upsert(p.readStage(dir), Staged{Name: name, Op: OpArchive, Base: rev, At: time.Now(), Why: strings.TrimSpace(why), Status: StatusPending})
	return p.writeStage(dir, list)
}

// Unstage drops card's change to name, whatever its status: a session
// withdrawing it, or the curator having taken it into the pack.
func (p *Pack) Unstage(card, name string) error {
	dir, err := p.stageDir(card)
	if err != nil {
		return err
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("no skill %q", name)
	}
	defer p.lock()()
	list := p.readStage(dir)
	n := len(list)
	list = slices.DeleteFunc(list, func(s Staged) bool { return s.Name == name })
	if len(list) == n {
		return fmt.Errorf("card %s has staged nothing for %q", card, name)
	}
	_ = os.RemoveAll(filepath.Join(dir, "skills", name))
	return p.writeStage(dir, list)
}

// Reject keeps card's change to name with the curator's reason; it no
// longer reaches the card's sessions, which may stage it again.
func (p *Pack) Reject(card, name, reason string) error {
	dir, err := p.stageDir(card)
	if err != nil {
		return err
	}
	defer p.lock()()
	list := p.readStage(dir)
	i := slices.IndexFunc(list, func(s Staged) bool { return s.Name == name })
	if i < 0 {
		return fmt.Errorf("card %s has staged nothing for %q", card, name)
	}
	list[i].Status, list[i].Verdict = StatusRejected, strings.TrimSpace(reason)
	return p.writeStage(dir, list)
}

// StagedFor lists card's changes, pending and rejected, by name.
func (p *Pack) StagedFor(card string) []Staged {
	dir, err := p.stageDir(card)
	if err != nil {
		return nil
	}
	return p.readStage(dir)
}

// StageCards lists every card holding a staged change, pending or
// rejected, with its changes.
func (p *Pack) StageCards() map[string][]Staged {
	out := map[string][]Staged{}
	if p.StageDir == "" {
		return out
	}
	entries, err := os.ReadDir(p.StageDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			if list := p.readStage(filepath.Join(p.StageDir, e.Name())); len(list) > 0 {
				out[e.Name()] = list
			}
		}
	}
	return out
}

// AllStaged lists every card's changes that wait for review.
func (p *Pack) AllStaged() map[string][]Staged {
	out := map[string][]Staged{}
	if p.StageDir == "" {
		return out
	}
	entries, err := os.ReadDir(p.StageDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var pending []Staged
		for _, s := range p.readStage(filepath.Join(p.StageDir, e.Name())) {
			if s.Pending() {
				pending = append(pending, s)
			}
		}
		if len(pending) > 0 {
			out[e.Name()] = pending
		}
	}
	return out
}

// StagedDraft reads card's staged write of name back as a draft.
func (p *Pack) StagedDraft(card, name string) (Draft, error) {
	dir, err := p.stageDir(card)
	if err != nil {
		return Draft{}, err
	}
	if !nameRE.MatchString(name) {
		return Draft{}, fmt.Errorf("no skill %q", name)
	}
	skill := filepath.Join(dir, "skills", name)
	md, err := os.ReadFile(filepath.Join(skill, "SKILL.md"))
	if err != nil {
		return Draft{}, fmt.Errorf("card %s has staged no write of %q", card, name)
	}
	m, body, err := Parse(md)
	if err != nil {
		return Draft{}, err
	}
	d := Draft{Name: name, Description: m.Description, Scope: m.Scope, Body: body, Files: map[string]string{}}
	err = filepath.WalkDir(skill, func(path string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() || !de.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(skill, path)
		if err != nil || rel == "SKILL.md" {
			return err
		}
		raw, err := os.ReadFile(path) //nolint:gosec // inside the card's stage
		if err != nil {
			return err
		}
		d.Files[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	return d, err
}

// StagedFile reads one file of card's staged write of name.
func (p *Pack) StagedFile(card, name, rel string) (string, bool) {
	dir, err := p.stageDir(card)
	if err != nil || !nameRE.MatchString(name) || !safeRel(rel) {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "skills", name, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// Overlay returns the pack card's sessions read: base (a materialized
// revision, or "" where the pack has none) with card's pending changes
// laid over it — its staged writes in, its staged archives out. With
// nothing pending it is base itself. A composed view is made once per
// content, under the card's stage, and kept until the card is released:
// a live session may still read an older one.
func (p *Pack) Overlay(base, card string) (string, error) {
	dir, err := p.stageDir(card)
	if err != nil {
		return base, nil //nolint:nilerr // a card that cannot stage reads the pack as it is
	}
	var pending []Staged
	for _, s := range p.readStage(dir) {
		if s.Pending() {
			pending = append(pending, s)
		}
	}
	if len(pending) == 0 {
		return base, nil
	}
	h := sha256.New()
	h.Write([]byte(base))
	for _, s := range pending {
		fmt.Fprintf(h, "\x00%s\x00%s\x00%d", s.Name, s.Op, s.At.UnixNano())
	}
	dest := filepath.Join(dir, "views", hex.EncodeToString(h.Sum(nil))[:16])
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".tmp-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	skip := map[string]bool{}
	for _, s := range pending {
		skip[s.Name] = true
	}
	if base != "" {
		entries, err := os.ReadDir(base)
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name() == archiveDir || strings.HasPrefix(e.Name(), ".") || skip[e.Name()] {
				continue
			}
			if err := copyTree(filepath.Join(base, e.Name()), filepath.Join(tmp, e.Name())); err != nil {
				return "", err
			}
		}
	}
	for _, s := range pending {
		if s.Op != OpWrite {
			continue
		}
		if err := copyTree(filepath.Join(dir, "skills", s.Name), filepath.Join(tmp, s.Name)); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec // read by sessions
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		if _, serr := os.Stat(dest); serr == nil {
			return dest, nil
		}
		return "", err
	}
	return dest, nil
}

// ReleaseStage removes the composed views of every card live does not
// hold. Its staged changes stay: they wait for review whether or not the
// card still runs.
func (p *Pack) ReleaseStage(live func(card string) bool) {
	if p.StageDir == "" {
		return
	}
	entries, err := os.ReadDir(p.StageDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || live(e.Name()) {
			continue
		}
		dir := filepath.Join(p.StageDir, e.Name())
		_ = os.RemoveAll(filepath.Join(dir, "views"))
		if len(p.readStage(dir)) == 0 {
			_ = os.RemoveAll(dir)
		}
	}
}

// copyTree copies the regular files and directories under src to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(out, 0o755) //nolint:gosec // read by sessions
		case !d.Type().IsRegular():
			return nil
		}
		raw, err := os.ReadFile(path) //nolint:gosec // inside the pack or a card's stage
		if err != nil {
			return err
		}
		return os.WriteFile(out, raw, 0o644) //nolint:gosec // read by sessions
	})
}
