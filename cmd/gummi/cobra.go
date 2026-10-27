package main

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// This file wires the top-level commands and their nested subcommands onto
// the cobra tree. Cobra owns routing, help, completion AND parsing: each
// command's flags are declared once, in the bind functions at the bottom,
// and its RunE hands the body a cliFlags view of what cobra parsed. See
// flags.go for why there is exactly one declaration site.

// doctorCmd implements `gummi doctor [--json] [--deep]`.
var doctorCmd = &cobra.Command{
	Use:   "doctor [--json] [--deep]",
	Short: "Run a readiness checklist for the workspace",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runDoctor(cmdFlags(cmd))
	},
}

// runCmd implements `gummi run "<prompt>"`: one session, driven to its end
// with no person at the keyboard.
var runCmd = &cobra.Command{
	Use:   `run --envelope <dollars> [--workflow name | --workflow-file path] [--input name=value ...] [--profile name] [--repo name] [--base branch] [--ref id] [--timeout d] [--verbose] ["<prompt>"]`,
	Short: "Run one session from a prompt, or one workflow on its inputs, unattended, streaming NDJSON (for evals and CI)",
	Long: `Run mints a session from the prompt and drives it to its end with no one at
the keyboard. Its workflow runs are always allowed, the envelope their only
cap. A question is answered on your behalf with its recommended option and
the answer is put on the stream; a revised plan is declined and a loop at its
max carries on. Money is never decided: a spent envelope stops the run.

The stream is NDJSON on stdout. The exit status is 0 when the session is done,
1 on an error, 2 when a person paused its workflow run from the board, 3 when
the envelope is spent and 4 on --timeout. The session stays on the board,
with its branch, for a person to read and land.

With --workflow (a workspace's or a bundled recipe) or --workflow-file, gummi
runs that workflow itself, with no session turn before it. Its inputs are
--input name=value (name=@file reads a file, name=- reads stdin; repeat it for
each) and --inputs, a YAML or JSON file of names to text that --input
overrides; an input the recipe does not declare, or a required one left out,
is refused before any card is minted. The run is judged against --spec, a
markdown spec, or else against a spec gummi writes from the prompt and the
inputs, with a done-when item for each --check. Without --spec, or with
--no-session, the run's report is the result: it is kept from the session,
and the drive ends with the run. Its end is a workflow_ended line carrying the
report, each artifact and the steps' DECISION: and OPEN: lines, and a run that
ends partial exits 5.`,
	Example: `  gummi run --envelope 5 "tidy the README"
  gummi run --envelope 2 --workflow triage --input report=@bug.md --input logs=@bug.log
  gh issue view 42 --json body -q .body | gummi run --envelope 2 --workflow triage --input report=-
  gummi run --envelope 10 --workflow deliver --spec feature.md`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRun(cmdFlags(cmd), args)
	},
}

// webCmd implements `gummi web`: the board in a browser, hosted by this
// process with the TUI's own model running without a screen.
var webCmd = &cobra.Command{
	Use:   "web [--addr host:port] [--allow-host names] [--tls-cert file --tls-key file] [--tailscale [--ts-hostname name] [--ts-authkey key] [--ts-tls] [--verbose]] [--no-pairing] [--terminal]",
	Short: "Serve the board to a browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWeb(cmdFlags(cmd), args)
	},
}

var webPairCmd = &cobra.Command{
	Use:   "pair [--name person]",
	Short: "Print a pairing code from the running web board",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runWebPair(cmdFlags(cmd))
	},
}

var webDevicesCmd = &cobra.Command{
	Use:   "devices [--json]",
	Short: "List the browsers paired with this board, and any waiting to be let in",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runWebDevices(cmdFlags(cmd))
	},
}

var webUnpairCmd = &cobra.Command{
	Use:   "unpair <id> | --all",
	Short: "Revoke a paired browser, or withdraw one waiting to be let in",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebUnpair(cmdFlags(cmd), args)
	},
}

func init() {
	bindDoctorFlags(doctorCmd.Flags())
	bindRunFlags(runCmd.Flags())
	bindWebFlags(webCmd.Flags())
	bindWebPairFlags(webPairCmd.Flags())
	jsonFlag(webDevicesCmd.Flags(), "emit the paired devices as JSON")
	webUnpairCmd.Flags().Bool("all", false, "unpair every device")

	webCmd.AddCommand(webPairCmd, webDevicesCmd, webUnpairCmd)
}

func bindDoctorFlags(fs *pflag.FlagSet) {
	jsonFlag(fs, "emit the readiness checklist as JSON (the skill's setup path)")
	fs.Bool("deep", false, "probe per-role model reachability with a live backend turn (TTL-cached)")
}

// bindRunFlags declares `gummi run`'s flags.
func bindRunFlags(fs *pflag.FlagSet) {
	fs.String("envelope", "", "the session's budget in dollars, its workflow runs' included (required; falls back to GUMMI_ENVELOPE)")
	fs.String("profile", "", "the model-role profile the session runs under (default: the workspace's)")
	fs.String("repo", "", "the configured repository the session works in (default: the workspace's)")
	fs.String("base", "", "the branch the session's work forks from (default: what the repository has checked out)")
	fs.String("ref", "", "an external id kept on the session and echoed on the created line (an eval case, a CI job)")
	fs.Duration("timeout", defaultRunTimeout, "stop when nothing has happened for this long (0: never)")
	fs.Bool("verbose", false, "add the session's tool-call activity lines to the stream")
	fs.String("workflow", "", "run this recipe itself, a workspace's or a bundled one, instead of sending the prompt")
	fs.String("workflow-file", "", "run the recipe written in this YAML file itself")
	fs.StringArray("input", nil, "an input of the workflow as name=value, name=@file or name=- (stdin); repeat for each")
	fs.String("inputs", "", "a YAML or JSON file mapping the workflow's input names to their text (--input overrides one)")
	fs.String("spec", "", "a markdown spec the workflow run is judged against (default: one gummi writes from the prompt and inputs)")
	fs.StringArray("check", nil, "a done-when command for the spec gummi writes; repeat for each (refused beside --spec)")
	fs.Bool("no-session", false, "keep the workflow run's report from the session and end with the run (the default without --spec)")
}

// bindWebFlags declares `gummi web`'s flags.
func bindWebFlags(fs *pflag.FlagSet) {
	fs.String("addr", "", "address to serve the board on (default "+defaultWebAddr+"; falls back to GUMMI_WEB_ADDR)")
	fs.String("allow-host", "", "comma-separated names the board also answers to, e.g. a reverse proxy's (`tailscale serve`); "+
		"it always answers to its own address, localhost, the --addr name, the certificate's names and the tailnet's")
	fs.String("tls-cert", "", "serve HTTPS with this certificate (PEM; needs --tls-key)")
	fs.String("tls-key", "", "the private key for --tls-cert (PEM)")
	fs.Bool("tailscale", false, "also serve on your tailnet as its own node (embedded tsnet; no tailscaled, no port forwarding)")
	fs.String("ts-hostname", defaultTSHostname, "the board's node name on your tailnet (with --tailscale)")
	fs.String("ts-authkey", "", "tailnet auth key instead of a browser login (with --tailscale); a flag shows in ps, "+
		"so prefer TS_AUTHKEY in the environment, read when the flag is not given")
	fs.Bool("ts-tls", false, "serve HTTPS on 443 with a tailnet certificate (with --tailscale; needs MagicDNS and HTTPS enabled for the tailnet)")
	fs.Bool("verbose", false, "log the tailnet node's own messages (with --tailscale)")
	fs.Bool("no-pairing", false, "serve without pairing, to anything that can reach the listener (refused unless every listener is loopback)")
	fs.Bool("terminal", false, "give paired devices a Terminal tab: a shell in each card's worktree, running as you (refused with --no-pairing)")
}

func bindWebPairFlags(fs *pflag.FlagSet) {
	fs.String("name", "", "pair the browser that redeems the code as this person")
}
