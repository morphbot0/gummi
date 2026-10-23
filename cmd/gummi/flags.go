package main

// The flag surface, declared once.
//
// Every verb's flags are declared here, on the cobra command's own
// pflag.FlagSet, and a command body reads them back through cliFlags. That
// single site is the point: cobra owns routing, help, completion AND
// parsing, and `gummi skill` generates its command grammar from the same
// tree, so what --help advertises, what the parser accepts, and what the
// skill documents cannot disagree.
//
// They used to disagree. Each verb declared its flags twice — once as
// pflag for cobra, once as a stdlib flag.FlagSet inside runXxx — and cobra
// re-serialized its parsed flags back into a []string for that second
// parser to read. Nothing kept the two copies honest: eight help strings
// had drifted (`gummi research --help` promised `--until shape`, which the
// binary rejects), `stack new --name` and `stack add --pos` were declared
// only on the inner set and so were unreachable, and `merge --m` was
// documented in SKILL.md and rejected by the binary. The round trip also
// lost "was this flag passed at all", which one verb had to patch back by
// hand.

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/morphis/gummi/internal/domain"
)

// cliFlags is a command body's view of the flags cobra has already parsed.
// The getters panic on an unknown name or a type mismatch: both are wiring
// mistakes in this package, not conditions a user can produce, and every
// command's flags are walked by the grammar generator and its tests — so a
// typo fails the build's tests rather than reaching a terminal.
type cliFlags struct{ fs *pflag.FlagSet }

// cmdFlags views the flags cobra parsed for cmd.
func cmdFlags(cmd *cobra.Command) cliFlags { return cliFlags{fs: cmd.Flags()} }

func (f cliFlags) String(name string) string {
	v, err := f.fs.GetString(name)
	if err != nil {
		panic(fmt.Sprintf("reading --%s as a string: %v", name, err))
	}
	return v
}

func (f cliFlags) Int(name string) int {
	v, err := f.fs.GetInt(name)
	if err != nil {
		panic(fmt.Sprintf("reading --%s as an int: %v", name, err))
	}
	return v
}

// Budget reads a dollar flag (budgetFlag) as the credits it was parsed to.
func (f cliFlags) Budget(name string) int {
	g := f.fs.Lookup(name)
	if g == nil {
		panic(fmt.Sprintf("--%s is not a flag of this command", name))
	}
	v, ok := g.Value.(*dollarsValue)
	if !ok {
		panic(fmt.Sprintf("reading --%s as a budget: it is a %s flag", name, g.Value.Type()))
	}
	return int(*v)
}

// dollarsValue is a budget flag: typed in dollars ("5", "$12.50"), held
// in credits, the unit every envelope is stored in.
type dollarsValue int

func (v *dollarsValue) String() string {
	return domain.DollarsInput(int(*v))
}

func (v *dollarsValue) Set(s string) error {
	n, err := domain.ParseDollars(s)
	if err != nil {
		return err
	}
	*v = dollarsValue(n)
	return nil
}

func (*dollarsValue) Type() string { return "dollars" }

// envelopeFlag declares --envelope on fs: a budget typed in dollars and
// read back in credits, defaulting to 0.
func envelopeFlag(fs *pflag.FlagSet, usage string) {
	fs.Var(new(dollarsValue), "envelope", usage)
}

func (f cliFlags) Bool(name string) bool {
	v, err := f.fs.GetBool(name)
	if err != nil {
		panic(fmt.Sprintf("reading --%s as a bool: %v", name, err))
	}
	return v
}

func (f cliFlags) Duration(name string) time.Duration {
	v, err := f.fs.GetDuration(name)
	if err != nil {
		panic(fmt.Sprintf("reading --%s as a duration: %v", name, err))
	}
	return v
}

// opt reads a string flag the command may or may not declare, reporting ""
// when it does not: a resumed session can change neither its repository
// nor its base branch, so resume declares neither.
func (f cliFlags) opt(name string) string {
	if f.fs.Lookup(name) == nil {
		return ""
	}
	return f.String(name)
}

// Changed reports whether the flag was present on the command line, so an
// explicitly empty value (`--answer ""`) is distinguishable from an unset
// one. This is what the old stdlib round trip could not carry: a []string
// rebuilt from parsed values cannot say "the user typed this".
func (f cliFlags) Changed(name string) bool {
	g := f.fs.Lookup(name)
	if g == nil {
		panic(fmt.Sprintf("--%s is not a flag of this command", name))
	}
	return g.Changed
}

// --- the shared driving surface ---------------------------------------

// driveFlags is the flag surface the session-driving verbs share — run
// and resume. Each field is that flag's help text; an empty field omits
// the flag from the verb entirely.
//
// Name, type and default live here once, so those cannot drift between
// verbs. Help text is per-verb on purpose: resume's --envelope raises a
// budget where run's sets one, and stating the difference at the call
// site makes it visible.
type driveFlags struct {
	envelope string
	profile  string
	timeout  string
	verbose  string
	ref      string
	repo     string
	base     string
}

// stdDriveFlags is `gummi run`'s wording; resume starts from it and
// overrides the entries whose meaning differs for it.
func stdDriveFlags() driveFlags {
	return driveFlags{
		envelope: "spend budget for the session, in dollars (required; falls back to GUMMI_ENVELOPE); its workflow runs are always allowed, and spend from it",
		profile:  "profile mapping roles to models (default: first configured)",
		timeout:  "stop as a timeout after this long with nothing happening on the session or its workflow run (0 disables)",
		verbose:  "add the session's per-tool-call activity lines to the stream",
		ref:      "external correlation id, echoed in the stream and persisted for status/resume lookup",
		repo:     "managed repository to start the session in (a configured repos: name; required when repos: is configured)",
		base:     "branch the session's work forks from and lands on (default: whatever the repository has checked out)",
	}
}

// bind declares whichever of the shared flags carry help text onto fs.
func (d driveFlags) bind(fs *pflag.FlagSet) {
	str := func(name, usage string) {
		if usage != "" {
			fs.String(name, "", usage)
		}
	}
	if d.envelope != "" {
		envelopeFlag(fs, d.envelope)
	}
	str("profile", d.profile)
	if d.timeout != "" {
		fs.Duration("timeout", defaultTimeout, d.timeout)
	}
	if d.verbose != "" {
		fs.Bool("verbose", false, d.verbose)
	}
	str("ref", d.ref)
	str("repo", d.repo)
	str("base", d.base)
}

// sharedDriveFlagNames are the flags driveFlags owns, in the order bind
// declares them. The skill's grammar generator factors exactly these out
// of the per-verb listings into one shared block.
var sharedDriveFlagNames = []string{
	"envelope", "profile", "timeout", "verbose", "ref", "repo", "base",
}

// messageFlag declares the -m/--message pair the three committing verbs
// share. They differ only in what the message is for, so that is the
// argument; the sentinel "-" reads the message from stdin everywhere.
//
// It is one flag with a shorthand, not the two separate long flags the
// stdlib flag package forced (`--m` plus a `--message` aliased onto it).
// That shim is why SKILL.md documented a `--m` the binary rejects.
func messageFlag(fs *pflag.FlagSet, what string) {
	fs.StringP("message", "m", "", what+" (required; - reads from stdin)")
}

// jsonFlag declares the --json switch the read-only verbs share.
func jsonFlag(fs *pflag.FlagSet, what string) {
	fs.Bool("json", false, what)
}

// commitMessage reads the -m/--message value for one of the committing
// verbs, resolving the "-" sentinel from stdin. required says whether an
// empty message is an error: it is everywhere except a goal's merge, whose
// commit message gummi writes from the goal and its cards.
func commitMessage(fl cliFlags, verb string, required bool) (string, error) {
	msg := fl.String("message")
	switch {
	case msg == "" && required:
		return "", fmt.Errorf("%s needs a commit message: pass -m <message> (or -m - to read one from stdin)", verb)
	case msg == "":
		return "", nil
	case msg == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading commit message from stdin: %w", err)
		}
		return string(b), nil
	}
	return msg, nil
}
