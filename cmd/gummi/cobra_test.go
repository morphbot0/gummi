package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs fn with the process-wide stdout redirected to a pipe and
// returns everything written to it. The cobra commands print version and help
// via os.Stdout (OutOrStdout), so swapping stdout is the reliable way to
// assert on their output in-process.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// captureStderr mirrors captureStdout for os.Stderr — used to assert on
// warnings that deliberately print outside a command's normal stdout
// output.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// resetRootHelpFlag clears cobra's --help value on the shared rootCmd.
// Execute triggers InitDefaultHelpFlag, which adds "help" as a local
// bool flag on rootCmd the first time it runs; pflag then only calls
// Set() on flags actually present in the parsed argv, so a single
// --help run leaves that flag's value true forever after on this
// package-level rootCmd. Every later Execute call that doesn't repeat
// --help still finds helpVal true and silently prints help instead of
// acting on its own args — that's what broke TestCobraVersionFlag under
// -shuffle=on when it ran after a --help test: cobra returned the help
// text instead of the version stamp. Any test that runs rootCmd with
// --help must reset this afterward (t.Cleanup, so it fires even if a
// later assertion in the same test fails first) rather than relying on
// test order to keep the pollution from mattering.
func resetRootHelpFlag(t *testing.T) {
	t.Helper()
	if f := rootCmd.Flags().Lookup("help"); f != nil {
		if err := f.Value.Set("false"); err != nil {
			t.Fatalf("resetting rootCmd's help flag: %v", err)
		}
	}
}

// The `version` subcommand prints the same stamp as the old dispatch.
func TestCobraVersion(t *testing.T) {
	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"version"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(version): %v", err)
		}
	})
	want := fmt.Sprintf("gummi %s\n", version())
	if out != want {
		t.Fatalf("version output = %q, want %q", out, want)
	}
}

// The root --version / -v flags short-circuit to the same stamp.
func TestCobraVersionFlag(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		out := captureStdout(t, func() {
			rootCmd.SetArgs(args)
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute(%v): %v", args, err)
			}
		})
		want := fmt.Sprintf("gummi %s\n", version())
		if out != want {
			t.Fatalf("%v output = %q, want %q", args, out, want)
		}
	}
}

// --help shows the hierarchical command list cobra derives from the tree.
func TestCobraHelp(t *testing.T) {
	t.Cleanup(func() { resetRootHelpFlag(t) })
	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"--help"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(--help): %v", err)
		}
	})
	if !strings.Contains(out, "Available Commands:") {
		t.Fatalf("help output missing the command list:\n%s", out)
	}
}

// An unknown command errors (cobra's unknown-command path).
func TestCobraUnknownCommand(t *testing.T) {
	rootCmd.SetArgs([]string{"frobnicate"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("Execute(frobnicate): want error, got nil")
	}
}

// BG-003: a flag passed with its default value is still a flag passed —
// `resume --answer ""` is an (empty) answer, which the driver refuses,
// not a bare resume that silently carries on.
//
// This used to be a hazard worth a dedicated workaround. Cobra parsed the
// flag, a helper re-serialized the parsed flags into a []string for a
// second, stdlib parser, and that serializer dropped any flag whose value
// equalled its default — because a []string cannot carry "the user typed
// this". There is one parser now, and pflag's Changed carries the fact
// directly.
func TestResumeKeepsAnExplicitDefaultFlag(t *testing.T) {
	resetFlags(rootCmd)
	t.Cleanup(func() { resetFlags(rootCmd) })

	cmd, _, err := rootCmd.Find([]string{"resume"})
	if err != nil {
		t.Fatalf("finding resume: %v", err)
	}
	if err := cmd.Flags().Set("answer", ""); err != nil {
		t.Fatalf("Set(answer, \"\"): %v", err)
	}
	if !cmdFlags(cmd).Changed("answer") {
		t.Fatal("Changed(answer) = false after an explicit --answer \"\"")
	}
	in, err := resumeInput(cmdFlags(cmd), nil)
	if err != nil || in.Answer == nil || *in.Answer != "" {
		t.Fatalf("resumeInput = %+v, %v; want an empty answer", in, err)
	}
}

// Every flag a command advertises must be parsed by that same command.
//
// This is now structural — cobra both declares and parses — so the test
// guards the structure rather than a second copy of it: no command body
// may build its own flag set. Two parsers is what let `gummi stack new
// --name`, `gummi stack add --pos` and `gummi merge --m` be declared,
// advertised by --help or documented in SKILL.md, and then rejected at
// parse with "unknown flag".
func TestNoCommandParsesItsOwnFlags(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "gummi")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if bytes.Contains(b, []byte("flag.NewFlagSet")) {
			t.Errorf("%s builds its own flag.FlagSet; cobra already parsed the command line — "+
				"declare the flags in cobra.go and read them through cliFlags, or the two sets will drift", name)
		}
	}
}

// Every flag bound on the tree must be one the skill's grammar generator
// can render, and every command the grammar names must be on the tree.
// mustFindCmd panics on a path that does not resolve, so rendering the
// grammar at all is the assertion.
func TestSkillGrammarResolvesAgainstTheTree(t *testing.T) {
	if got := commandGrammar(); got == "" {
		t.Fatal("commandGrammar() is empty")
	}
}

// TestInitCmdRegistered catches a forgotten rootCmd.AddCommand(initCmd) the
// way a missed registration would silently drop the verb.
func TestInitCmdRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"init"})
	if err != nil {
		t.Fatalf("rootCmd.Find([\"init\"]): %v", err)
	}
	if cmd != initCmd {
		t.Errorf("rootCmd.Find([\"init\"]) resolved to %v, want initCmd", cmd)
	}
}
