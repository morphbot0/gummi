package skillpack

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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

	mu sync.Mutex
}

// New returns the pack of a workspace whose .gummi directory is gummiDir
// and whose state directory is stateDir.
func New(gummiDir, stateDir string) *Pack {
	return &Pack{Dir: filepath.Join(gummiDir, "skills"), Cache: filepath.Join(stateDir, "skillpacks")}
}

// identity is gummi's own, unsigned and past any hook, like an agent
// home's: a machine with no git identity or a signing key that needs a
// person can still keep a pack.
var identity = []string{"-c", "user.name=gummi", "-c", "user.email=gummi@localhost", "-c", "commit.gpgsign=false"}

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
	p.mu.Lock()
	defer p.mu.Unlock()
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

// Write lints d against the pack's tip and, if it passes, replaces the
// skill's directory with it in one commit and publishes. The lint is
// returned either way; a refused write changes nothing.
func (p *Pack) Write(ctx context.Context, d Draft, repos []string, message string) (Lint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensure(ctx); err != nil {
		return Lint{}, err
	}
	existing, err := p.Skills(ctx, "HEAD")
	if err != nil {
		return Lint{}, err
	}
	l := LintDraft(d, existing, repos)
	if !l.OK() {
		return l, nil
	}
	dir := filepath.Join(p.Dir, d.Name)
	if err := os.RemoveAll(dir); err != nil {
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
			return l, err
		}
		if err := os.WriteFile(path, c, 0o644); err != nil { //nolint:gosec // read by sessions
			return l, err
		}
	}
	return l, p.commit(ctx, message, d.Name)
}

// Archive moves curator skills under archive/ in one commit and
// publishes. A person's or a pinned skill is refused; nothing is deleted.
func (p *Pack) Archive(ctx context.Context, names []string, message string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensure(ctx); err != nil {
		return err
	}
	existing, err := p.Skills(ctx, "HEAD")
	if err != nil {
		return err
	}
	var paths []string
	for _, name := range names {
		m, ok := existing[name]
		switch {
		case !ok:
			return fmt.Errorf("no skill %q to archive", name)
		case !m.Curated():
			return fmt.Errorf("%q is a person's or pinned skill; the curator never archives one", name)
		}
		dest := filepath.Join(archiveDir, name)
		if _, err := os.Stat(filepath.Join(p.Dir, dest)); err == nil {
			dest = filepath.Join(archiveDir, name+"-"+strconv.FormatInt(time.Now().Unix(), 10))
		}
		if err := os.MkdirAll(filepath.Join(p.Dir, archiveDir), 0o755); err != nil { //nolint:gosec // read by sessions
			return err
		}
		if _, err := p.git(ctx, "mv", name, dest); err != nil {
			return err
		}
		paths = append(paths, name, dest)
	}
	return p.commit(ctx, message, paths...)
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

// Revert undoes the commits in (from, to] — one curator run, or one write
// when from is to's parent — in one commit, and publishes. Reverting a run
// a later run built on can conflict; the pack is then left as it was.
func (p *Pack) Revert(ctx context.Context, from, to, message string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if from == "" {
		from = to + "^"
	}
	if _, err := p.git(ctx, "revert", "--no-commit", from+".."+to); err != nil {
		_, _ = p.git(ctx, "revert", "--abort")
		if strings.Contains(err.Error(), "could not revert") {
			return fmt.Errorf("%w: a later commit changed the same skills (%s); the pack is unchanged — revert that first, or edit the pack by hand", ErrRevertConflict, p.laterTouching(ctx, from, to))
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

// laterTouching names the commits after to that touch what (from, to]
// touched: what stands in the way of reverting it.
func (p *Pack) laterTouching(ctx context.Context, from, to string) string {
	files, err := p.git(ctx, "diff", "--name-only", from, to)
	if err != nil || files == "" {
		return "unknown"
	}
	out, err := p.git(ctx, append([]string{"log", "--format=%h %s", to + "..HEAD", "--"}, strings.Fields(files)...)...)
	if err != nil || out == "" {
		return "unknown"
	}
	return strings.ReplaceAll(out, "\n", "; ")
}

// Pin marks a skill pinned (or not), in a commit of the person's, and
// publishes: the curator and the timers leave a pinned skill alone.
func (p *Pack) Pin(ctx context.Context, name string, pinned bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
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

// Born returns when each skill at HEAD was first written: its SKILL.md's
// oldest commit. The timers measure an unused skill from here.
func (p *Pack) Born(ctx context.Context, names []string) map[string]time.Time {
	born := map[string]time.Time{}
	for _, name := range names {
		out, err := p.git(ctx, "log", "--format=%ct", "--", name+"/SKILL.md")
		if err != nil || out == "" {
			continue
		}
		lines := strings.Fields(out)
		sec, _ := strconv.ParseInt(lines[len(lines)-1], 10, 64)
		born[name] = time.Unix(sec, 0)
	}
	return born
}

// Materialize extracts rev into Cache/<rev> once and returns that
// directory. Revisions are immutable, so a session that started on one
// keeps reading it while a curator run publishes the next; the oldest are
// pruned past keep.
func (p *Pack) Materialize(ctx context.Context, rev string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
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
	p.prune(dest)
	return dest, nil
}

// keep is how many materialized revisions stay on disk. A session reads
// the one it started with; ten publishes during one session's life is
// far past what a daily curator makes.
const keep = 10

func (p *Pack) prune(current string) {
	entries, err := os.ReadDir(p.Cache)
	if err != nil {
		return
	}
	type rev struct {
		path string
		mod  time.Time
	}
	var revs []rev
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			revs = append(revs, rev{filepath.Join(p.Cache, e.Name()), fi.ModTime()})
		}
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].mod.After(revs[j].mod) })
	for i, r := range revs {
		if i >= keep && r.path != current {
			_ = os.RemoveAll(r.path)
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
