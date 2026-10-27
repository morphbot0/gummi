package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/driver"
	"github.com/morphis/gummi/internal/state"
)

// defaultRunTimeout bounds how long a run may hear nothing from its
// session or the session's workflow run before it stops as a likely hang.
// Generous: a strong model on a large change can think for a long time
// between events. --timeout 0 disables it.
const defaultRunTimeout = 20 * time.Minute

// runRun implements `gummi run [flags] "<prompt>"`: one session minted from
// the prompt and driven to its end with no person at the keyboard (package
// driver), streaming NDJSON on stdout and exiting with the end's status.
// The envelope is required, and an agent must start: both fail before any
// card is minted.
//
// With --workflow or --workflow-file it runs that workflow itself on the
// inputs given, and the prompt is optional.
func runRun(fl cliFlags, args []string) error {
	opts, err := runOptions(fl)
	if err != nil {
		return err
	}
	prompt := ""
	switch {
	case len(args) == 1:
		prompt = args[0]
	case len(args) > 1 || !opts.RunsWorkflow():
		if opts.RunsWorkflow() {
			return errors.New("run takes at most one prompt argument")
		}
		return errors.New("run needs exactly one prompt argument")
	}
	// A run is routinely launched detached (a CI step, nohup): the hangup
	// of a terminal it no longer needs must not kill it and the agent it
	// drives. SIGINT and SIGTERM still stop it, as an error on the stream.
	signal.Ignore(syscall.SIGHUP)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	wsRoot, defaultRoot, named, err := resolveAllRoots(cwd)
	if err != nil {
		return err
	}
	ws, err := ensureWorkspace(wsRoot, defaultRoot)
	if err != nil {
		return err
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		return err
	}
	defer store.Close()
	pool, err := newPool(ctx, wsRoot, defaultRoot, named, store, true)
	if err != nil {
		return err
	}
	hookd := wireHooks(store, pool, ws)
	defer hookd.Close()
	eng, agents, _, why, err := engineFromEnv(store, pool, ws)
	if err != nil {
		return err
	}
	if eng == nil {
		return fmt.Errorf("no coding agent could start (%s); a run needs one — see `gummi doctor`", why)
	}
	defer func() { _ = eng.Close(); closeAgents(agents) }()
	// the engine holds the card it mints, and each step's card, for the
	// length of the drive: a board open on the workspace sees the session
	// but cannot drive it at the same time
	eng.UseCardLocks(state.NewCardLocks(ws))

	out, _ := driver.New(eng, store, ws, os.Stdout, opts).Run(ctx, prompt)
	if code := out.Status.ExitCode(); code != 0 {
		// the stream has told the story; exit with its status, quietly
		return &exitError{code: code}
	}
	return nil
}

// runOptions validates and assembles run's options. The envelope falls
// back to GUMMI_ENVELOPE, then refuses.
func runOptions(fl cliFlags) (driver.Options, error) {
	raw := fl.String("envelope")
	if raw == "" {
		raw = os.Getenv("GUMMI_ENVELOPE")
	}
	if raw == "" {
		return driver.Options{}, errors.New("an envelope is required: pass --envelope <dollars> (or set GUMMI_ENVELOPE)")
	}
	envelope, err := domain.ParseDollars(raw)
	if err != nil || envelope <= 0 {
		return driver.Options{}, fmt.Errorf("envelope %q is not a positive dollar amount", raw)
	}
	opts := driver.Options{
		Envelope: envelope, Profile: fl.String("profile"),
		Repo: fl.String("repo"), Base: fl.String("base"), Ref: fl.String("ref"),
		Timeout: fl.Duration("timeout"), Verbose: fl.Bool("verbose"),
	}
	return workflowOptions(fl, opts, os.Stdin)
}

// workflowOptions reads the flags that make a run start a workflow itself:
// the recipe, its inputs, the spec or its checks. Any of the last three
// without a recipe is refused, as is reading stdin twice.
func workflowOptions(fl cliFlags, opts driver.Options, stdin io.Reader) (driver.Options, error) {
	opts.Workflow = strings.TrimSpace(fl.String("workflow"))
	file := fl.String("workflow-file")
	if opts.Workflow != "" && file != "" {
		return opts, errors.New("name a recipe with --workflow or give one with --workflow-file, not both")
	}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return opts, fmt.Errorf("--workflow-file: %w", err)
		}
		opts.WorkflowYAML = string(b)
	}
	inputs, specFile, checks := fl.StringArray("input"), fl.String("spec"), fl.StringArray("check")
	if !opts.RunsWorkflow() {
		if len(inputs) > 0 || fl.String("inputs") != "" || specFile != "" || len(checks) > 0 || fl.Bool("no-session") {
			return opts, errors.New("--input, --inputs, --spec, --check and --no-session need a recipe: pass --workflow or --workflow-file")
		}
		return opts, nil
	}
	given, err := readInputs(fl.String("inputs"), inputs, stdin)
	if err != nil {
		return opts, err
	}
	opts.Inputs, opts.Checks = given, checks
	if specFile != "" {
		b, err := os.ReadFile(specFile)
		if err != nil {
			return opts, fmt.Errorf("--spec: %w", err)
		}
		if strings.TrimSpace(string(b)) == "" {
			return opts, fmt.Errorf("--spec: %s is empty", specFile)
		}
		opts.Spec = string(b)
	}
	// without a spec of its own the run's report is the product: nothing
	// is left for the session to read and commit
	opts.NoSession = fl.Bool("no-session") || specFile == ""
	return opts, nil
}

// readInputs reads a workflow's inputs: the --inputs file's, then each
// --input over them. An --input is name=value, name=@file or name=- for
// stdin, which only one may read.
func readInputs(file string, flags []string, stdin io.Reader) (map[string]string, error) {
	out := map[string]string{}
	if file != "" {
		b, err := os.ReadFile(strings.TrimPrefix(file, "@"))
		if err != nil {
			return nil, fmt.Errorf("--inputs: %w", err)
		}
		// YAML reads JSON too
		var m map[string]string
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("--inputs: %s is not a mapping of input names to text: %w", file, err)
		}
		for k, v := range m {
			out[k] = v
		}
	}
	readStdin := false
	for _, kv := range flags {
		name, value, ok := strings.Cut(kv, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("--input %q: write it as name=value, name=@file or name=-", kv)
		}
		switch {
		case value == "-":
			if readStdin {
				return nil, errors.New("--input: only one input may read stdin")
			}
			readStdin = true
			b, err := io.ReadAll(stdin)
			if err != nil {
				return nil, fmt.Errorf("--input %s: reading stdin: %w", name, err)
			}
			value = string(b)
		case strings.HasPrefix(value, "@"):
			b, err := os.ReadFile(value[1:])
			if err != nil {
				return nil, fmt.Errorf("--input %s: %w", name, err)
			}
			value = string(b)
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// closeAgents closes every distinct agent in the map exactly once (the ""
// default key aliases one of the concrete-name entries).
func closeAgents(agents map[string]agent.Agent) {
	seen := map[agent.Agent]struct{}{}
	for _, a := range agents {
		if a == nil {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		_ = a.Close()
	}
}
