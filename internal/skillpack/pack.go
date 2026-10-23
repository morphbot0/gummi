package skillpack

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/morphis/gummi/internal/atomicfile"
)

// PublishedRef is what sessions read. Commits land on the pack's branch;
// gummi moves this ref after a write passes lint, and a revert moves it
// with the revert's commit. Nothing reads the working tree.
const PublishedRef = "refs/gummi/published"

// Pack is the skill pack repository at Dir, materialized for sessions
// under Cache.
type Pack struct {
	Dir   string // .gummi/skills
	Cache string // .gummi/state/skillpacks
	// StageDir holds each card's staged changes (stage.go).
	StageDir string // .gummi/state/skillstage
	// BuiltinDir holds gummi's own skills, written out (builtin.go).
	BuiltinDir string // .gummi/state/skillbuiltin
	// Missing, when set, names the paths of paths that the base branch of
	// the repository a scope names does not have: the grounding lint. A
	// general skill names no repository and is not checked.
	Missing func(ctx context.Context, scope string, paths []string) []string

	mu sync.Mutex
}

// New returns the pack of a workspace whose .gummi directory is gummiDir
// and whose state directory is stateDir.
func New(gummiDir, stateDir string) *Pack {
	return &Pack{
		Dir:        filepath.Join(gummiDir, "skills"),
		Cache:      filepath.Join(stateDir, "skillpacks"),
		StageDir:   filepath.Join(stateDir, "skillstage"),
		BuiltinDir: filepath.Join(stateDir, "skillbuiltin"),
	}
}

// identity is gummi's own, unsigned and past any hook, like an agent
// home's: a machine with no git identity or a signing key that needs a
// person can still keep a pack.
var identity = []string{"-c", "user.name=gummi", "-c", "user.email=gummi@localhost", "-c", "commit.gpgsign=false"}

// lock holds the pack for one write, against this process's other
// goroutines and against every other gummi process on the workspace (a
// board and a CLI's pin or revert), and returns its release.
func (p *Pack) lock() func() {
	p.mu.Lock()
	release := flock(p.Dir + ".lock")
	return func() {
		release()
		p.mu.Unlock()
	}
}

func (p *Pack) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", p.Dir}, args...)...) //nolint:gosec // git, with arguments gummi builds
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Exists reports whether the pack has been made.
func (p *Pack) Exists() bool {
	fi, err := os.Lstat(filepath.Join(p.Dir, ".git"))
	return err == nil && fi.IsDir()
}

// Ensure makes the pack on first use: a repository with one empty commit,
// published. A .git that is a file (a repository somewhere else) is
// refused, as an agent home's is.
func (p *Pack) Ensure(ctx context.Context) error {
	defer p.lock()()
	return p.ensure(ctx)
}

func (p *Pack) ensure(ctx context.Context) error {
	if err := os.MkdirAll(p.Dir, 0o755); err != nil { //nolint:gosec // sessions read the pack through their backends
		return err
	}
	switch fi, err := os.Lstat(filepath.Join(p.Dir, ".git")); {
	case os.IsNotExist(err):
		if _, err := p.git(ctx, "init", "--quiet", "--initial-branch=main"); err != nil {
			return err
		}
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("%s names a repository somewhere else", filepath.Join(p.Dir, ".git"))
	}
	if _, err := p.git(ctx, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		args := append(slices.Clone(identity), "commit", "--quiet", "--no-verify", "--allow-empty", "-m", "an empty skill pack")
		if _, err := p.git(ctx, args...); err != nil {
			return err
		}
	}
	if _, err := p.git(ctx, "rev-parse", "--verify", "--quiet", PublishedRef); err != nil {
		return p.publish(ctx)
	}
	return nil
}

// Published returns the revision sessions read, or "" when the pack has
// not been made.
func (p *Pack) Published(ctx context.Context) string {
	if !p.Exists() {
		return ""
	}
	rev, err := p.git(ctx, "rev-parse", "--verify", "--quiet", PublishedRef)
	if err != nil {
		return ""
	}
	return rev
}

func (p *Pack) publish(ctx context.Context) error {
	_, err := p.git(ctx, "update-ref", PublishedRef, "HEAD")
	return err
}

// Head returns the pack branch's tip.
func (p *Pack) Head(ctx context.Context) (string, error) { return p.git(ctx, "rev-parse", "HEAD") }

// Subjects is every commit subject on the pack's history, newest first.
func (p *Pack) Subjects(ctx context.Context) ([]string, error) {
	out, err := p.git(ctx, "log", "--format=%s")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// Skills reads every live skill at rev (archive/ excluded), by name. A
// SKILL.md that does not parse is left out: it reaches no session.
func (p *Pack) Skills(ctx context.Context, rev string) (map[string]Meta, error) {
	out, err := p.git(ctx, "ls-tree", "--name-only", rev)
	if err != nil {
		return nil, err
	}
	skills := map[string]Meta{}
	for _, name := range strings.Fields(out) {
		if name == archiveDir || strings.HasPrefix(name, ".") {
			continue
		}
		md, err := p.git(ctx, "show", rev+":"+name+"/SKILL.md")
		if err != nil {
			continue
		}
		m, _, err := Parse([]byte(md + "\n"))
		if err != nil {
			continue
		}
		if m.Name == "" {
			m.Name = name
		}
		skills[name] = m
	}
	return skills, nil
}

// Show returns one skill's SKILL.md at rev.
func (p *Pack) Show(ctx context.Context, rev, name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", fmt.Errorf("no skill %q", name)
	}
	return p.git(ctx, "show", rev+":"+name+"/SKILL.md")
}

// ShowFile returns one file of a skill at rev, by its path in the skill.
func (p *Pack) ShowFile(ctx context.Context, rev, name, rel string) (string, error) {
	if !nameRE.MatchString(name) || !safeRel(rel) {
		return "", fmt.Errorf("no file %q in skill %q", rel, name)
	}
	return p.git(ctx, "show", rev+":"+name+"/"+filepath.ToSlash(rel))
}

// Write lints d against the pack's tip and, if it passes, replaces the
// skill's directory with it in one commit and publishes. The lint is
// returned either way; a refused write changes nothing.
func (p *Pack) Write(ctx context.Context, d Draft, repos []string, message string) (Lint, error) {
	defer p.lock()()
	if err := p.ensure(ctx); err != nil {
		return Lint{}, err
	}
	existing, err := p.Skills(ctx, "HEAD")
	if err != nil {
		return Lint{}, err
	}
	l := LintDraft(d, existing, repos)
	replaces := slices.Compact(slices.Sorted(slices.Values(d.Replaces)))
	for _, name := range replaces {
		if name == d.Name {
			l.Refusals = append(l.Refusals, fmt.Sprintf("%q cannot replace itself; write it without replaces", name))
		} else if err := archivable(existing, name); err != nil {
			l.Refusals = append(l.Refusals, err.Error())
		}
	}
	if !l.OK() {
		return l, nil
	}
	if p.Missing != nil && d.Scope != "" && d.Scope != ScopeGeneral {
		if missing := p.Missing(ctx, d.Scope, RepoPaths(d.Body)); len(missing) > 0 {
			l.Warnings = append(l.Warnings, fmt.Sprintf("the %s repository's base branch has no %s; check the path is right", d.Scope, strings.Join(missing, ", ")))
		}
	}
	dir := filepath.Join(p.Dir, d.Name)
	if err := os.RemoveAll(dir); err != nil {
		p.restore(ctx, d.Name)
		return l, err
	}
	scope := d.Scope
	if scope == "" {
		scope = ScopeGeneral
	}
	md := Render(Meta{Name: d.Name, Description: strings.TrimSpace(d.Description), Scope: scope, Source: SourceCurator}, d.Body)
	files := map[string][]byte{"SKILL.md": md}
	for rel, c := range d.Files {
		files[rel] = []byte(c)
	}
	for rel, c := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // read by sessions
			p.restore(ctx, d.Name)
			return l, err
		}
		if err := os.WriteFile(path, c, 0o644); err != nil { //nolint:gosec // read by sessions
			p.restore(ctx, d.Name)
			return l, err
		}
	}
	paths, err := p.moveToArchive(ctx, replaces)
	paths = append(paths, d.Name)
	if err != nil {
		p.restore(ctx, paths...)
		return l, err
	}
	if err := p.commit(ctx, message, paths...); err != nil {
		p.restore(ctx, paths...)
		return l, err
	}
	return l, nil
}

// archivable is why the curator may not archive name, or nil.
func archivable(existing map[string]Meta, name string) error {
	m, ok := existing[name]
	switch {
	case !ok:
		return fmt.Errorf("no skill %q to archive", name)
	case !m.Curated():
		return fmt.Errorf("%q is a person's or pinned skill; the curator never archives one", name)
	}
	return nil
}

// moveToArchive stages each of names moved under archive/ and returns
// every path it touched, the ones moved so far when it fails.
func (p *Pack) moveToArchive(ctx context.Context, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Join(p.Dir, archiveDir), 0o755); err != nil { //nolint:gosec // read by sessions
		return nil, err
	}
	var paths []string
	for _, name := range names {
		dest := filepath.Join(archiveDir, name)
		if _, err := os.Stat(filepath.Join(p.Dir, dest)); err == nil {
			dest = filepath.Join(archiveDir, name+"-"+strconv.FormatInt(time.Now().Unix(), 10))
		}
		paths = append(paths, name, dest)
		if _, err := p.git(ctx, "mv", name, dest); err != nil {
			return paths, err
		}
	}
	return paths, nil
}

// Archive moves curator skills under archive/ in one commit and
// publishes. A person's or a pinned skill is refused; nothing is deleted.
func (p *Pack) Archive(ctx context.Context, names []string, message string) error {
	defer p.lock()()
	if err := p.ensure(ctx); err != nil {
		return err
	}
	existing, err := p.Skills(ctx, "HEAD")
	if err != nil {
		return err
	}
	// Every name is checked before any is moved: one refusal leaves the
	// pack as it was, not with the names before it staged as moved.
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	for _, name := range names {
		if err := archivable(existing, name); err != nil {
			return err
		}
	}
	paths, err := p.moveToArchive(ctx, names)
	if err != nil {
		p.restore(ctx, paths...)
		return err
	}
	if err := p.commit(ctx, message, paths...); err != nil {
		p.restore(ctx, paths...)
		return err
	}
	return nil
}

// restore puts paths back as HEAD has them, in the index and on disk: a
// write or archive that failed partway leaves nothing staged for the next
// commit to sweep up. Best effort; a path HEAD lacks is just removed.
func (p *Pack) restore(ctx context.Context, paths ...string) {
	if len(paths) == 0 {
		return
	}
	_, _ = p.git(ctx, append([]string{"reset", "--quiet", "--"}, paths...)...)
	for _, path := range paths {
		if _, err := p.git(ctx, "cat-file", "-e", "HEAD:"+filepath.ToSlash(path)); err == nil {
			_, _ = p.git(ctx, "checkout", "--quiet", "HEAD", "--", path)
		}
	}
	_, _ = p.git(ctx, append([]string{"clean", "-fdq", "--"}, paths...)...)
}

// commit commits only paths (a person's uncommitted edits elsewhere in
// the pack stay theirs) and publishes.
func (p *Pack) commit(ctx context.Context, message string, paths ...string) error {
	// A path git mv took away is staged already, and not on disk to add.
	var add []string
	for _, path := range paths {
		if _, err := os.Lstat(filepath.Join(p.Dir, path)); err == nil {
			add = append(add, path)
		}
	}
	if len(add) > 0 {
		if _, err := p.git(ctx, append([]string{"add", "--all", "--"}, add...)...); err != nil {
			return err
		}
	}
	if _, err := p.git(ctx, "diff", "--cached", "--quiet", "--"); err == nil {
		return p.publish(ctx) // nothing changed
	}
	args := append(slices.Clone(identity), "commit", "--quiet", "--no-verify", "-m", message, "--")
	if _, err := p.git(ctx, append(args, paths...)...); err != nil {
		return err
	}
	return p.publish(ctx)
}

// Revert undoes the commits in (from, to] — or to alone when from is "" —
// in one commit, and publishes.
func (p *Pack) Revert(ctx context.Context, from, to, message string) error {
	if from == "" {
		from = to + "^"
	}
	out, err := p.git(ctx, "rev-list", from+".."+to)
	if err != nil {
		return err
	}
	return p.RevertCommits(ctx, strings.Fields(out), message)
}

// CommitsSince is the commits after from whose subject starts with prefix,
// newest first: one curator run's own, leaving out the sweep's and a
// person's pins that landed while it ran.
func (p *Pack) CommitsSince(ctx context.Context, from, prefix string) ([]string, error) {
	out, err := p.git(ctx, "log", "--format=%H%x00%s", from+"..HEAD")
	if err != nil {
		return nil, err
	}
	var commits []string
	for _, line := range strings.Split(out, "\n") {
		if hash, subject, ok := strings.Cut(line, "\x00"); ok && strings.HasPrefix(subject, prefix) {
			commits = append(commits, hash)
		}
	}
	return commits, nil
}

// RevertCommits undoes commits (newest first) in one commit, and
// publishes. Reverting a run a later commit built on can conflict; the
// pack is then left as it was.
func (p *Pack) RevertCommits(ctx context.Context, commits []string, message string) error {
	defer p.lock()()
	if len(commits) == 0 {
		return errors.New("nothing to revert")
	}
	if _, err := p.git(ctx, append([]string{"revert", "--no-commit"}, commits...)...); err != nil {
		_, _ = p.git(ctx, "revert", "--abort")
		if strings.Contains(err.Error(), "could not revert") {
			return fmt.Errorf("%w: a later commit changed the same skills (%s); the pack is unchanged — revert that first, or edit the pack by hand", ErrRevertConflict, p.laterTouching(ctx, commits))
		}
		return err
	}
	args := append(slices.Clone(identity), "commit", "--quiet", "--no-verify", "--allow-empty", "-m", message)
	if _, err := p.git(ctx, args...); err != nil {
		return err
	}
	return p.publish(ctx)
}

// ErrRevertConflict is a revert that later commits stand in the way of.
var ErrRevertConflict = errors.New("cannot revert cleanly")

// laterTouching names the commits since the oldest of commits, other
// than them, that touch what they touched: what stands in the way of
// reverting them.
func (p *Pack) laterTouching(ctx context.Context, commits []string) string {
	files, err := p.git(ctx, append([]string{"show", "--name-only", "--format="}, commits...)...)
	if err != nil || files == "" {
		return "unknown"
	}
	out, err := p.git(ctx, append([]string{"log", "--format=%H %h %s", commits[len(commits)-1] + "..HEAD", "--"}, strings.Fields(files)...)...)
	if err != nil {
		return "unknown"
	}
	var later []string
	for _, line := range strings.Split(out, "\n") {
		hash, rest, ok := strings.Cut(line, " ")
		if ok && !slices.Contains(commits, hash) {
			later = append(later, rest)
		}
	}
	if len(later) == 0 {
		return "unknown"
	}
	return strings.Join(later, "; ")
}

// Pin marks a skill pinned (or not), in a commit of the person's, and
// publishes: the curator and the timers leave a pinned skill alone.
func (p *Pack) Pin(ctx context.Context, name string, pinned bool) error {
	defer p.lock()()
	path := filepath.Join(p.Dir, name, "SKILL.md")
	md, err := os.ReadFile(path)
	if err != nil || !nameRE.MatchString(name) {
		return fmt.Errorf("no skill %q", name)
	}
	m, body, err := Parse(md)
	if err != nil {
		return err
	}
	m.Pinned = pinned
	if err := os.WriteFile(path, Render(m, body), 0o644); err != nil { //nolint:gosec // read by sessions
		return err
	}
	verb := "pin"
	if !pinned {
		verb = "unpin"
	}
	return p.commit(ctx, verb+" "+name, name)
}

// Entry is one commit of the pack's history.
type Entry struct {
	Commit  string
	When    time.Time
	Subject string
}

// Log returns the pack's newest n commits.
func (p *Pack) Log(ctx context.Context, n int) ([]Entry, error) {
	out, err := p.git(ctx, "log", "-n", strconv.Itoa(n), "--format=%H%x00%ct%x00%s")
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		sec, _ := strconv.ParseInt(parts[1], 10, 64)
		entries = append(entries, Entry{Commit: parts[0], When: time.Unix(sec, 0), Subject: parts[2]})
	}
	return entries, nil
}

// Born returns when each skill at HEAD last came into the pack: the
// newest commit that added its SKILL.md at that path. A skill brought
// back from archive/ (a revert, or a person's move) is born again then,
// so the timers do not count its time in the archive as time unused.
func (p *Pack) Born(ctx context.Context, names []string) map[string]time.Time {
	born := map[string]time.Time{}
	for _, name := range names {
		out, err := p.git(ctx, "log", "-n", "1", "--no-renames", "--diff-filter=A", "--format=%ct", "--", name+"/SKILL.md")
		if err != nil || out == "" {
			continue
		}
		sec, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		born[name] = time.Unix(sec, 0)
	}
	return born
}

// pinsFile is the revision each conversation reads, in Cache.
const pinsFile = "pins.json"

// revPin is the revision one conversation was given, and whose it is.
type revPin struct {
	Rev  string    `json:"rev"`
	Card string    `json:"card"`
	At   time.Time `json:"at"`
}

func (p *Pack) readPins() map[string]revPin {
	pins := map[string]revPin{}
	if raw, err := os.ReadFile(filepath.Join(p.Cache, pinsFile)); err == nil {
		_ = json.Unmarshal(raw, &pins)
	}
	return pins
}

func (p *Pack) writePins(pins map[string]revPin) error {
	raw, err := json.Marshal(pins)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.Cache, 0o755); err != nil { //nolint:gosec // read by sessions
		return err
	}
	return atomicfile.Write(filepath.Join(p.Cache, pinsFile), raw, 0o600)
}

// SessionDir returns the materialized pack a conversation reads, and its
// revision. key names the conversation (it outlives one backend process:
// a reopen, a resume, a switch of backend), card is whose it is. A
// conversation that is continuing keeps the revision it was first given;
// a new one is given the published revision, which it then keeps. "" is
// a pack that has not been made.
func (p *Pack) SessionDir(ctx context.Context, key, card string, continuing bool) (dir, rev string, err error) {
	unlock := p.lock()
	pins := p.readPins()
	pin, ok := pins[key]
	if !continuing || !ok || pin.Rev == "" {
		if rev = p.Published(ctx); rev == "" {
			unlock()
			return "", "", nil
		}
		pins[key] = revPin{Rev: rev, Card: card, At: time.Now()}
		if err := p.writePins(pins); err != nil {
			unlock()
			return "", "", err
		}
	} else {
		rev = pin.Rev
	}
	unlock()
	dir, err = p.Materialize(ctx, rev)
	return dir, rev, err
}

// Release drops the pins of every card live does not hold (finished or
// gone), then removes each materialized revision no pin reads and that is
// not published: a revision stays on disk exactly while a conversation
// may still read it.
func (p *Pack) Release(ctx context.Context, live func(card string) bool) {
	defer p.lock()()
	pins := p.readPins()
	changed := false
	for key, pin := range pins {
		if !live(pin.Card) {
			delete(pins, key)
			changed = true
		}
	}
	if changed {
		_ = p.writePins(pins)
	}
	p.prune(ctx, pins)
}

// Edits counts, for each skill at HEAD, the commits that changed its
// SKILL.md since it last came into the pack (Born), the add itself left out.
func (p *Pack) Edits(ctx context.Context, born map[string]time.Time) map[string]int {
	edits := map[string]int{}
	for name, at := range born {
		out, err := p.git(ctx, "log", "--no-renames", "--format=%ct", "--since="+strconv.FormatInt(at.Unix(), 10), "--", name+"/SKILL.md")
		if err != nil {
			continue
		}
		if n := len(strings.Fields(out)) - 1; n > 0 {
			edits[name] = n
		}
	}
	return edits
}

// Materialize extracts rev into Cache/<rev> once and returns that
// directory. Revisions are immutable, so a session that started on one
// keeps reading it while a curator run publishes the next.
func (p *Pack) Materialize(ctx context.Context, rev string) (string, error) {
	defer p.lock()()
	dest := filepath.Join(p.Cache, rev)
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", p.Dir, "archive", "--format=tar", rev) //nolint:gosec // rev is a hash gummi resolved
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git archive %s: %w: %s", rev, err, strings.TrimSpace(errb.String()))
	}
	tmp, err := os.MkdirTemp(p.Cache, ".tmp-*")
	if err != nil {
		if err := os.MkdirAll(p.Cache, 0o755); err != nil { //nolint:gosec // read by sessions
			return "", err
		}
		if tmp, err = os.MkdirTemp(p.Cache, ".tmp-*"); err != nil {
			return "", err
		}
	}
	if err := untar(&out, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec // read by sessions
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.RemoveAll(tmp)
		if _, serr := os.Stat(dest); serr == nil {
			return dest, nil
		}
		return "", err
	}
	return dest, nil
}

// prune removes the materialized revisions no pin reads, but the
// published one. A leftover temp directory from a crash goes too.
func (p *Pack) prune(ctx context.Context, pins map[string]revPin) {
	entries, err := os.ReadDir(p.Cache)
	if err != nil {
		return
	}
	read := map[string]bool{p.Published(ctx): true}
	for _, pin := range pins {
		read[pin.Rev] = true
	}
	for _, e := range entries {
		if e.IsDir() && !read[e.Name()] {
			_ = os.RemoveAll(filepath.Join(p.Cache, e.Name()))
		}
	}
}

func untar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Name == "pax_global_header" || !safeRel(strings.TrimSuffix(h.Name, "/")) {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil { //nolint:gosec // read by sessions
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // read by sessions
				return err
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // inside dir, path checked by safeRel
			if err != nil {
				return err
			}
			_, cerr := io.Copy(f, io.LimitReader(tr, 1<<20))
			_ = f.Close()
			if cerr != nil {
				return cerr
			}
		}
	}
}

// prefixedDir is where Prefixed keeps a materialized revision's renamed
// copy; DirsFor skips it, being a dot directory.
const prefixedDir = ".prefixed"

// Prefixed returns the copy of a materialized pack whose skills are named
// Prefix+<name>, directory and frontmatter alike, for a backend that names
// skills bare. A revision never changes, so the copy is made once, beside
// it, and goes when the revision is pruned.
func Prefixed(root string) (string, error) {
	dest := filepath.Join(root, prefixedDir)
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, ".tmp-prefixed-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, e := range entries {
		if !e.IsDir() || e.Name() == archiveDir || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := copyPrefixed(filepath.Join(root, e.Name()), filepath.Join(tmp, Prefix+e.Name())); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec // read by sessions
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		if _, serr := os.Stat(dest); serr == nil {
			return dest, nil // another session made it first
		}
		return "", err
	}
	return dest, nil
}

// copyPrefixed copies one skill directory, renaming the skill in its
// SKILL.md. A directory with no readable SKILL.md is not a skill and is
// left out.
func copyPrefixed(src, dst string) error {
	md, err := os.ReadFile(filepath.Join(src, "SKILL.md"))
	if err != nil {
		return nil
	}
	m, body, err := Parse(md)
	if err != nil {
		return nil
	}
	m.Name = Prefix + filepath.Base(src)
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
		case rel == "SKILL.md":
			return os.WriteFile(out, Render(m, body), 0o644) //nolint:gosec // read by sessions
		}
		raw, err := os.ReadFile(path) //nolint:gosec // inside a materialized revision
		if err != nil {
			return err
		}
		return os.WriteFile(out, raw, 0o644) //nolint:gosec // read by sessions
	})
}

// DirsFor returns the skill directories of a materialized pack that reach
// a session working in repo: every general skill, and repo's own.
func DirsFor(root, repo string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == archiveDir || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		md, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue
		}
		m, _, err := Parse(md)
		if err != nil || !m.Reaches(repo) {
			continue
		}
		dirs = append(dirs, dir)
	}
	return dirs
}
