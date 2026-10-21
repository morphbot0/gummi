package main

import (
	"github.com/morphis/gummi/internal/publish"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// This file wires the top-level commands and their nested subcommands onto
// the cobra tree. Cobra owns routing, help, completion AND parsing: each
// command's flags are declared once, in the bind functions at the bottom,
// and its RunE hands the body a cliFlags view of what cobra parsed. See
// flags.go for why there is exactly one declaration site.

// runCmd implements `gummi run [flags] "<prompt>"`.
var runCmd = &cobra.Command{
	Use:   "run [flags] \"<prompt>\"",
	Short: "Start a session from a prompt and drive it headlessly until it is done or needs you",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRun(cmdFlags(cmd), args)
	},
}

// resumeCmd implements `gummi resume <id|ref> [--answer <text> | "<message>"]`.
var resumeCmd = &cobra.Command{
	Use:   "resume <id|ref> [--answer <text> | \"<message>\"]",
	Short: "Answer a stopped session, or send it a turn, and drive it on",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runResume(cmdFlags(cmd), args)
	},
}

// mergeCmd implements `gummi merge <id|ref> -m <message|->`.
var mergeCmd = &cobra.Command{
	Use:   "merge <id|ref> -m <message|->",
	Short: "Headlessly land a verified branch as one squash commit",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMerge(cmdFlags(cmd), args)
	},
}

// squashCmd implements `gummi squash <id|ref> -m <message|->`.
var squashCmd = &cobra.Command{
	Use:   "squash <id|ref> -m <message|->",
	Short: "Collapse a card's branch to one commit, in place",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSquash(cmdFlags(cmd), args)
	},
}

// handoffCmd implements `gummi handoff <id|ref>`.
var handoffCmd = &cobra.Command{
	Use:   "handoff <id|ref>",
	Short: "Close a verified card without landing it — its branch is kept while it is the only copy of the work",
	RunE:  func(cmd *cobra.Command, args []string) error { return runHandOff(cliFlags{cmd.Flags()}, args) },
}

// cleanCmd implements `gummi clean <id|ref>` and `gummi clean --all`.
var cleanCmd = &cobra.Command{
	Use:   "clean <id|ref> | --all",
	Short: "Remove a landed card's worktree and branch, or every one at once",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runClean(cmdFlags(cmd), args)
	},
}

// commitCmd implements `gummi commit <id|ref> -m <message|->`.
var commitCmd = &cobra.Command{
	Use:   "commit <id|ref> -m <message|->",
	Short: "Commit a card's own uncommitted worktree changes onto its branch",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCommit(cmdFlags(cmd), args)
	},
}

// statusCmd implements `gummi status <id|ref> [--json] [--stats]`.
var statusCmd = &cobra.Command{
	Use:   "status <id|ref> [--json] [--stats]",
	Short: "Show a card's stage, spend, and branch state",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatus(cmdFlags(cmd), args)
	},
}

// watchCmd implements `gummi watch <id|ref> [--json] [--wait] [--once]`.
var watchCmd = &cobra.Command{
	Use:   "watch <id|ref> [--json] [--wait] [--once]",
	Short: "Follow the live agent stream of a card another gummi is driving",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWatch(cmdFlags(cmd), args)
	},
}

// specCmd implements `gummi spec <id|ref>`.
var specCmd = &cobra.Command{
	Use:   "spec <id|ref>",
	Short: "Dump a card's current design artifact",
	RunE:  func(_ *cobra.Command, args []string) error { return runSpec(args) },
}

// diffCmd implements `gummi diff <id|ref>`.
var diffCmd = &cobra.Command{
	Use:   "diff <id|ref>",
	Short: "Dump a card's worktree diff against its base branch",
	RunE:  func(_ *cobra.Command, args []string) error { return runDiff(args) },
}

// logCmd implements `gummi log <id|ref> [--json]`.
var logCmd = &cobra.Command{
	Use:   "log <id|ref>",
	Short: "List a card's own commits, oldest first",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLog(cmdFlags(cmd), args)
	},
}

// rewriteCmd implements `gummi rewrite <id|ref> --plan <file|->`.
var rewriteCmd = &cobra.Command{
	Use:   "rewrite <id|ref> [--plan <file|->] [--sign]",
	Short: "Reword, squash or sign a card's commits in place; its content never changes",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRewrite(cmdFlags(cmd), args)
	},
}

// doctorCmd implements `gummi doctor [--json] [--deep]`.
var doctorCmd = &cobra.Command{
	Use:   "doctor [--json] [--deep]",
	Short: "Run a readiness checklist for the workspace",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runDoctor(cmdFlags(cmd))
	},
}

// initCmd implements `gummi init`.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create and seed the .gummi workspace in the current directory",
	RunE:  func(_ *cobra.Command, _ []string) error { return runInit() },
}

// stackCmd groups the stack operations. A stack is a chain of cards
// whose branches fork from one another: slice one piece of work into
// several reviewable branches, land them bottom-first, and let gummi
// replay the ones above whenever a card below them changes.
var stackCmd = &cobra.Command{
	Use:   "stack",
	Short: "Chain cards so each one's branch forks from the one below it",
}

var stackNewCmd = &cobra.Command{
	Use:   "new <bottom-card> [--name <name>]",
	Short: "Start a stack from the card that sits at its bottom",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStackNew(cmdFlags(cmd), args)
	},
}

var stackAddCmd = &cobra.Command{
	Use:   "add <stack> <card> [--pos N]",
	Short: "Put a card into a stack",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStackAdd(cmdFlags(cmd), args)
	},
}

var stackRmCmd = &cobra.Command{
	Use:   "rm <card>",
	Short: "Take a card out of its stack",
	RunE:  func(_ *cobra.Command, args []string) error { return runStackRm(args) },
}

var stackMvCmd = &cobra.Command{
	Use:   "mv <card> <position>",
	Short: "Move a card within its stack (0 is the bottom)",
	RunE:  func(_ *cobra.Command, args []string) error { return runStackMv(args) },
}

var stackListCmd = &cobra.Command{
	Use:   "list [<stack>]",
	Short: "List the stacks, or one stack's cards bottom-first",
	RunE:  func(_ *cobra.Command, args []string) error { return runStackList(args) },
}

var stackRestackCmd = &cobra.Command{
	Use:   "restack <stack|card>",
	Short: "Replay every card in a stack onto its current base now",
	RunE:  func(_ *cobra.Command, args []string) error { return runStackRestack(args) },
}

// scheduleCmd groups the timed triggers for freeform sessions
// (DESIGN §19.9): a schedule mints a freeform card on a cron cadence, a
// heartbeat sends a recurring turn into one session. Store verbs — they
// run with or without a board; none of them fires a session from this
// process.
var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Schedules and heartbeats — freeform sessions that come back on a clock",
}

var scheduleListCmd = &cobra.Command{
	Use:   "list [--json]",
	Short: "List the schedules and their last outcome",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleList(cmdFlags(cmd), args)
	},
}

var scheduleAddCmd = &cobra.Command{
	Use:   `add --name <name> (--cron "<cron>"|--every <preset>) --prompt "<prompt>" (--envelope N | --heartbeat <FF-id>)`,
	Short: "Define a schedule or a heartbeat, stored off until enabled",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleAdd(cmdFlags(cmd), args)
	},
}

var scheduleEnableCmd = &cobra.Command{
	Use:   "enable <id|name>",
	Short: "Turn a schedule on; its first fire is computed from now",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleEnable(cmdFlags(cmd), args)
	},
}

var scheduleDisableCmd = &cobra.Command{
	Use:   "disable <id|name>",
	Short: "Turn a schedule off",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleDisable(cmdFlags(cmd), args)
	},
}

var scheduleRunNowCmd = &cobra.Command{
	Use:   "run-now <id|name>",
	Short: "Ask the running board to fire a schedule once, off-cadence",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScheduleRunNow(cmdFlags(cmd), args)
	},
}

var scheduleRmCmd = &cobra.Command{
	Use:   "rm <id|name>",
	Short: "Delete a schedule (the cards it minted stay)",
	RunE:  func(_ *cobra.Command, args []string) error { return runScheduleRm(args) },
}

// curatorCmd groups the workspace skill pack and its curator (DESIGN
// §23): what the pack holds, what each curator run did, and the person's
// after-the-fact controls — revert and pin. Only `run --wait` starts a
// session from this process, and only while no board is up.
var curatorCmd = &cobra.Command{
	Use:   "curator",
	Short: "The workspace skill pack and the curator that writes it",
}

var curatorStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the curator's config, budget and next run, and the pack's skills",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCuratorStatus(cmdFlags(cmd), args)
	},
}

var curatorLogCmd = &cobra.Command{
	Use:   "log [--json]",
	Short: "List the curator's runs: what each wrote, archived and cost",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCuratorLog(cmdFlags(cmd), args)
	},
}

var curatorRunCmd = &cobra.Command{
	Use:   "run [--wait]",
	Short: "Ask the running board to run the curator once, or run it here with --wait",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCuratorRun(cmdFlags(cmd), args)
	},
}

var curatorRevertCmd = &cobra.Command{
	Use:   "revert <run-id|commit>",
	Short: "Undo one curator run, or one pack commit, and publish the result",
	RunE:  func(_ *cobra.Command, args []string) error { return runCuratorRevert(args) },
}

var curatorPinCmd = &cobra.Command{
	Use:   "pin <skill>",
	Short: "Hold a skill where it is: the curator and the timers leave it alone",
	RunE:  func(_ *cobra.Command, args []string) error { return runCuratorPin(args, true) },
}

var curatorUnpinCmd = &cobra.Command{
	Use:   "unpin <skill>",
	Short: "Give a pinned curator skill back to the curator",
	RunE:  func(_ *cobra.Command, args []string) error { return runCuratorPin(args, false) },
}

// prCmd groups the outbound-PR operations (link/unlink/status).
var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Link, unlink, and check the outbound PR a card lands through",
}

var prLinkCmd = &cobra.Command{
	Use:   "link <card> <url|number> [--auto]",
	Short: "Link a card to an existing PR",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPRLink(cmdFlags(cmd), args)
	},
}

var prUnlinkCmd = &cobra.Command{
	Use:   "unlink <card>",
	Short: "Clear a card's linked PR",
	RunE:  func(_ *cobra.Command, args []string) error { return runPRUnlink(args) },
}

var prStatusCmd = &cobra.Command{
	Use:   "status <card> [--json]",
	Short: "Show a card's linked PR state and comment count",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPRStatus(cmdFlags(cmd), args)
	},
}

var prCommentsCmd = &cobra.Command{
	Use:   "comments <card> [--ingest] [--json]",
	Short: "List or ingest a linked PR's unresolved review threads as diff annotations",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPRComments(cmdFlags(cmd), args)
	},
}

// The publish verbs (DESIGN §22): a person pushes a card's branch and opens,
// updates or readies its PR through gh. Each prints the resolved facts and
// the exact commands and runs them once confirmed — at the terminal, or with
// --yes=<fingerprint> of the facts read. They are left out of the agent
// skill bundle and refuse inside a gummi session.
var pushCmd = &cobra.Command{
	Use:   "push <card> [--remote <name>] [--draft] [--yes <facts>]",
	Short: "Push a card's branch to its remote (a person's act)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPublish(cmdFlags(cmd), publish.ActPush, args)
	},
}

var prCreateCmd = &cobra.Command{
	Use:   "create <card> [--title <t>] [--body-file <f|->] [--draft] [--yes <facts>]",
	Short: "Push a card's branch and open its pull request (a person's act)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPublish(cmdFlags(cmd), publish.ActCreate, args)
	},
}

var prUpdateCmd = &cobra.Command{
	Use:   "update <card> [--title <t>] [--body-file <f|->] [--draft] [--yes <facts>]",
	Short: "Push new commits to a card's PR and edit its title or body (a person's act)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPublish(cmdFlags(cmd), publish.ActUpdate, args)
	},
}

var prReadyCmd = &cobra.Command{
	Use:   "ready <card> [--yes <facts>]",
	Short: "Mark a card's draft PR ready for review, at a verified tip (a person's act)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPublish(cmdFlags(cmd), publish.ActReady, args)
	},
}

var prDraftCmd = &cobra.Command{
	Use:   "draft <card> [--yes <facts>]",
	Short: "Turn a card's PR back into a draft (a person's act)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPublish(cmdFlags(cmd), publish.ActDraft, args)
	},
}

// skillCmd groups the skill file operations.
var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Show, install, or list the gummi agent skill",
}

var skillShowCmd = &cobra.Command{
	Use:   "show [<file>]",
	Short: "Print the rendered SKILL.md, or one of its reference files",
	RunE:  func(_ *cobra.Command, args []string) error { return skillShow(args) },
}

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the skill bundle for an agent",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return skillInstall(cmdFlags(cmd))
	},
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "Report each install target's state",
	RunE:  func(_ *cobra.Command, _ []string) error { return skillList() },
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
	bindRunFlags(runCmd.Flags())
	bindResumeFlags(resumeCmd.Flags())
	bindMergeFlags(mergeCmd.Flags())
	bindHandoffFlags(handoffCmd.Flags())
	bindSquashFlags(squashCmd.Flags())
	bindCommitFlags(commitCmd.Flags())
	bindCleanFlags(cleanCmd.Flags())
	jsonFlag(logCmd.Flags(), "emit the commits as JSON (the shape the board's web page reads)")
	bindRewriteFlags(rewriteCmd.Flags())
	bindStatusFlags(statusCmd.Flags())
	bindWatchFlags(watchCmd.Flags())
	bindDoctorFlags(doctorCmd.Flags())
	bindStackNewFlags(stackNewCmd.Flags())
	bindStackAddFlags(stackAddCmd.Flags())
	bindScheduleAddFlags(scheduleAddCmd.Flags())
	bindScheduleEnableFlags(scheduleEnableCmd.Flags())
	bindScheduleDisableFlags(scheduleDisableCmd.Flags())
	bindScheduleRunNowFlags(scheduleRunNowCmd.Flags())
	bindScheduleListFlags(scheduleListCmd.Flags())
	jsonFlag(curatorStatusCmd.Flags(), "emit the status as JSON")
	jsonFlag(curatorLogCmd.Flags(), "emit the runs as JSON")
	curatorRunCmd.Flags().Bool("wait", false, "run the curator in this process and wait for it (only while no board is up)")
	bindPRLinkFlags(prLinkCmd.Flags())
	bindPRStatusFlags(prStatusCmd.Flags())
	bindPRCommentsFlags(prCommentsCmd.Flags())
	for _, c := range []*cobra.Command{pushCmd, prCreateCmd, prUpdateCmd, prReadyCmd, prDraftCmd} {
		bindPublishFlags(c, c.Flags())
	}
	bindSkillInstallFlags(skillInstallCmd.Flags())
	bindWebFlags(webCmd.Flags())
	bindWebPairFlags(webPairCmd.Flags())
	jsonFlag(webDevicesCmd.Flags(), "emit the paired devices as JSON")
	webUnpairCmd.Flags().Bool("all", false, "unpair every device")

	stackCmd.AddCommand(stackNewCmd, stackAddCmd, stackRmCmd, stackMvCmd, stackListCmd, stackRestackCmd)
	scheduleCmd.AddCommand(scheduleListCmd, scheduleAddCmd, scheduleEnableCmd, scheduleDisableCmd, scheduleRunNowCmd, scheduleRmCmd)
	curatorCmd.AddCommand(curatorStatusCmd, curatorLogCmd, curatorRunCmd, curatorRevertCmd, curatorPinCmd, curatorUnpinCmd)
	prCmd.AddCommand(prLinkCmd, prUnlinkCmd, prStatusCmd, prCommentsCmd, prCreateCmd, prUpdateCmd, prReadyCmd, prDraftCmd)
	skillCmd.AddCommand(skillShowCmd, skillInstallCmd, skillListCmd)
	webCmd.AddCommand(webPairCmd, webDevicesCmd, webUnpairCmd)
}

// bindRunFlags declares `gummi run`'s flags: the shared driving surface.
func bindRunFlags(fs *pflag.FlagSet) {
	stdDriveFlags().bind(fs)
}

// bindResumeFlags declares `gummi resume`'s flags: the shared driving
// surface, worded for a session that already exists, and the answer.
func bindResumeFlags(fs *pflag.FlagSet) {
	d := stdDriveFlags()
	d.envelope = "raise the session's budget, and its crew pre-authorisation with it, in dollars (clears an exhausted stop; never lowers it)"
	d.ref = "external correlation id, echoed in the stream"
	// A resumed session already knows its repository, its base branch and
	// the profile it was minted with; none of the three can change now.
	d.profile, d.repo, d.base = "", "", ""
	d.bind(fs)

	fs.String("answer", "", "answer the session's open question, or its crew's open decision (one of the offered options, or your own words)")
}

func bindCleanFlags(fs *pflag.FlagSet) {
	fs.Bool("all", false, "clean every closed card in the workspace in one pass, instead of naming one")
}

// keepBranchFlag is the one opt-out the closing verbs share: a closed
// card's worktree and branch go with it unless this is given.
func keepBranchFlag(fs *pflag.FlagSet) {
	fs.Bool("keep-branch", false, "keep the card's worktree and branch after it closes (default: both are removed, except a branch that is the only copy of its work)")
}

func bindHandoffFlags(fs *pflag.FlagSet) { keepBranchFlag(fs) }

func bindMergeFlags(fs *pflag.FlagSet) {
	fs.StringP("message", "m", "", "landing commit message (required for a squash; - reads from stdin)")
	fs.Bool("no-squash", false, "land as a merge commit keeping the branch's commits (default: one squash commit); -m is optional and git's merge message is used without it")
	keepBranchFlag(fs)
}

func bindCommitFlags(fs *pflag.FlagSet) {
	messageFlag(fs, "commit message for the card's uncommitted worktree changes")
}

func bindSquashFlags(fs *pflag.FlagSet) {
	messageFlag(fs, "collapsed commit message")
	fs.Bool("force", false, "proceed even if the linked PR has open review threads")
}

// bindRewriteFlags declares `gummi rewrite`'s flags.
func bindRewriteFlags(fs *pflag.FlagSet) {
	fs.String("plan", "", `the branch as it should read, oldest first (a file path, or - for stdin): {"head":"<tip>","groups":[{"commits":["<sha>",…],"message":"…"},…]} — every commit in exactly one group`)
	fs.Bool("dry-run", false, "say what the plan would do and move nothing")
	fs.Bool("sign", false, "sign the card's commits: every commit from the first unsigned one up is made again, signed; alone it needs no --plan. Refused where commits are not signed")
	fs.Bool("allow-pushed", false, "rewrite commits the remote already has; the branch will then need a force push, which gummi prints and never runs")
}

// bindStatusFlags declares `gummi status`'s flags.
//
// --stats is opt-in rather than always on because it reads the card's
// whole event log, and status is a thing callers poll. Where the card
// stands stays a cheap question; how it got there is the expensive one.
func bindStatusFlags(fs *pflag.FlagSet) {
	jsonFlag(fs, "emit machine-readable JSON instead of the text summary")
	fs.Bool("stats", false, "report where the card's money and hours went instead of where it stands")
}

func bindWatchFlags(fs *pflag.FlagSet) {
	jsonFlag(fs, "emit the raw record stream as NDJSON instead of the rendered transcript")
	fs.Bool("wait", false, "block until the card has a live stream instead of failing when none exists")
	fs.Bool("once", false, "exit when the current session ends instead of following the card's next one")
}

func bindDoctorFlags(fs *pflag.FlagSet) {
	jsonFlag(fs, "emit the readiness checklist as JSON (the skill's setup path)")
	fs.Bool("deep", false, "probe per-role model reachability with a live backend turn (TTL-cached)")
}

func bindStackNewFlags(fs *pflag.FlagSet) {
	fs.String("name", "", "the stack's display name (default: the bottom card's slug)")
}

func bindStackAddFlags(fs *pflag.FlagSet) {
	fs.Int("pos", -1, "position in the stack, 0 at the bottom (default: the top)")
}

// bindScheduleAddFlags declares `gummi schedule add`'s flags. A mint and
// a heartbeat are the same command's two shapes: the mint takes repo,
// agent/model and its envelope (the brake); the heartbeat names the one
// card it sends turns to.
func bindScheduleAddFlags(fs *pflag.FlagSet) {
	fs.String("name", "", "the schedule's display name; its id is the slug of it (required)")
	fs.String("cron", "", "the cadence, as a 5-field cron expression (minute hour day month weekday)")
	fs.String("every", "", "the cadence, as a preset compiled to cron: 5m, 15m, 1h, 6h, @hourly, @daily, @weekly")
	fs.String("tz", "", "the cadence's timezone (IANA name; default: this host's)")
	fs.String("prompt", "", "what fires: a mint's opening turn, or the heartbeat's recurring turn (required)")
	fs.String("heartbeat", "", "heartbeat: the freeform card (FF-NNN) to send the prompt to, instead of minting")
	fs.String("repo", "", "mint: managed repository the card is minted in (a configured repos: name; default: the workspace default)")
	fs.String("agent", "", "mint: the session's backend (default: the profile's implementer)")
	fs.String("model", "", "mint: the session's model (default: the profile's)")
	envelopeFlag(fs, "mint: the minted card's spend brake, in dollars (required; every card mints with one)")
	jsonFlag(fs, "emit the stored row as JSON (the shape the board's web page reads)")
}

func bindScheduleEnableFlags(fs *pflag.FlagSet) {
	jsonFlag(fs, "emit the row as JSON (the shape the board's web page reads)")
}

func bindScheduleDisableFlags(fs *pflag.FlagSet) {
	jsonFlag(fs, "emit the row as JSON (the shape the board's web page reads)")
}

func bindScheduleRunNowFlags(fs *pflag.FlagSet) {
	jsonFlag(fs, "emit the row as JSON (the shape the board's web page reads)")
}

func bindScheduleListFlags(fs *pflag.FlagSet) {
	jsonFlag(fs, "emit the rows as JSON (the shape the board's web page reads)")
}

func bindPRLinkFlags(fs *pflag.FlagSet) {
	fs.Bool("auto", false, "resolve the PR whose head branch matches the card's branch (via gh pr list --head)")
}

func bindPRStatusFlags(fs *pflag.FlagSet) {
	jsonFlag(fs, "emit machine-readable JSON instead of the text summary")
}

func bindPRCommentsFlags(fs *pflag.FlagSet) {
	fs.Bool("ingest", false, "write an annotation per unresolved review thread onto the card's diff")
	jsonFlag(fs, "emit machine-readable JSON instead of the text summary")
}

func bindPublishFlags(cmd *cobra.Command, fs *pflag.FlagSet) {
	fs.String("yes", "", "run without the terminal confirm: the fingerprint of the facts you read (printed as `facts`)")
	jsonFlag(fs, "emit the facts, the plan and the result as JSON; without --yes nothing is run")
	switch cmd {
	case pushCmd:
		fs.String("remote", "", "push to this remote, remembered as the branch's pushRemote")
		fs.Bool("draft", false, "when the linked PR is ready and the tip is not verified, turn it back into a draft first")
	case prCreateCmd, prUpdateCmd:
		if cmd == prCreateCmd {
			fs.String("repo", "", "open the PR in this repository (`owner/name`): a fork's own or its parent's, remembered once the PR opens")
		}
		fs.String("title", "", "the PR's title (create: default from the card and its commits)")
		fs.String("body-file", "", "read the PR's body from this file, or - for stdin")
		fs.Bool("draft", false, "create: open as a draft; update: turn a ready PR back into a draft before pushing an unverified tip")
	}
}

func bindSkillInstallFlags(fs *pflag.FlagSet) {
	fs.String("agent", "", "target a specific agent: "+skillAgentList+" (default: detect)")
	fs.String("scope", "", "install scope: project|user (default: project, or ask when interactive)")
	fs.Bool("force", false, "overwrite an existing skill bundle (default: refuse and warn on drift)")
	fs.Bool("dry-run", false, "print what would be written, change nothing")
	fs.Bool("check", false, "verify every target is up to date; write nothing, fail if any is absent/foreign/drifted")
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
