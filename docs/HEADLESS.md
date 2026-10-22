# Driving gummi headlessly

The board is one way in. The other is the non-interactive driver: the same
engine, with nobody at the keyboard. This page is the full reference for that
path. The README carries the short version, and `gummi skill show` prints the
loop as a calling agent reads it.

`gummi run` starts a **session** — a coding agent on a branch and worktree of
its own (DESIGN §19) — from a prompt, and keeps it moving. The session may
run a **workflow** (DESIGN §25): a recipe of steps, each a fresh-context
agent, working in the session's own tree against one of its specs. The run
leaves its changes in the tree, uncommitted, and its report comes back to the
session as a turn. A person running `gummi run` always allows the session's
runs: nothing estimates what a run will spend, and the envelope is the only
cap. What a run cannot settle by itself comes back to the caller. `run` and `resume` never merge: landing is the separate
`merge` verb.

## Starting a session

```sh
gummi run --envelope 5 "Add a --format=json flag to the export command"
```

Two things must be true before any work begins, and both fail loud:

- an envelope is named, in dollars (`--envelope 5`, `$12.50`, or
  `GUMMI_ENVELOPE`). The board's creation form prefills $20 you can edit; an
  unattended run has no one to read a default, so it must name one.
- an agent backend is configured. `gummi doctor` tells you which.

The run mints the session, always allows its workflow runs (a run it
proposes starts without asking, and spends from the envelope until that is
spent), sends the prompt
as the session's first turn, and then waits on the session and its workflow
run. It stops when the session is idle with no run open or queued (`done`),
or when something needs a person.

Other `run` flags:

| flag | purpose |
|---|---|
| `--ref <id>` | your own tracker id, echoed in the stream. `status` and `resume` accept it in place of the card id |
| `--repo <name>` | which managed repository to start the session in. Required when `repos:` is configured |
| `--base <branch>` | the branch the session's work forks from and lands on |
| `--profile <name>` | the profile mapping roles to models |
| `--timeout <dur>` | stop as a `timeout` after this long with nothing happening on the session or its workflow run; 0 disables (default 20m) |
| `--verbose` | add the session's per-tool-call activity lines to the stream |

Wherever a verb takes a card as `<id>`, its number alone works too:
`gummi status 42` is `gummi status FF-042`. The stream and `--json` always
carry the full id. One flag still wants the id itself:
`schedule --heartbeat`.

## Verbs

| command | purpose |
|---|---|
| `gummi run [flags] "<prompt>"` | start a session from a prompt and drive it until it is done or needs you |
| `gummi resume <id\|ref> [--answer <text> \| "<message>"]` | answer a stopped session (or its workflow run), or send it a turn, and drive on |
| `gummi status <id\|ref> [--json]` | where a card is: blockers, spend, branch state |
| `gummi status <id\|ref> --stats [--json]` | how the card ran: each pass and what it cost, and the share of its life spent waiting on a person |
| `gummi watch <id\|ref> [--json] [--wait] [--once]` | follow the live agent stream of a card another gummi is driving |
| `gummi spec <id\|ref> [--spec <n>] [--list]` | a session's spec markdown: `--spec` picks one of its specs by number (needed once it has more than one), `--list` lists them with each one's state, revision and title |
| `gummi diff <id\|ref>` | the worktree diff against its base |
| `gummi merge <id\|ref> -m <message\|-> [--no-squash] [--keep-branch]` | land a branch as one squash commit, or with `--no-squash` as a merge commit keeping its commits, where `-m` is optional and git's own merge message is used |
| `gummi squash <id\|ref> -m <message\|->` | collapse a card's branch to one commit in place |
| `gummi commit <id\|ref> -m <message\|->` | commit a card's own uncommitted worktree changes onto its branch |
| `gummi handoff <id\|ref> [--keep-branch]` | close a card without landing it; its branch is kept while it is the only copy of the work |
| `gummi clean <id\|ref>` \| `gummi clean --all` | remove a landed or closed card's worktree and branch (an adopted card keeps its branch, and so does a closed card whose branch is the only copy of its work); `--all` does this for every closed card in one pass instead of naming one |
| `gummi pr link\|unlink\|status\|comments <id> [flags]` | link a card to a PR you opened, or read its status and review comments |
| `gummi push <id>`, `gummi pr create\|update\|ready\|draft <id> [flags]` | a person publishes a card: push its branch, open or update its PR, mark it ready or draft. Prints the facts and commands and asks at the terminal; without one, `--yes=<fingerprint>` of facts already printed. Refused inside a gummi-spawned session (DESIGN §22) |
| `gummi init` | create and seed the `.gummi` workspace without opening the board |
| `gummi doctor [--json] [--deep]` | readiness: repo, backend, auth, profile, envelope, lock, per-role reach |
| `gummi skill show\|install\|list` | the calling-agent skill |

`status`, `watch`, `spec` and `diff` take no lock, so you can inspect a card
while a run is live. Everything that drives or writes a card (`run`,
`resume`, `merge`, `squash`, `commit`, `clean`) holds that card's own lock for
the length of the command — not a lock on the whole `.gummi` workspace.
Headless runs on different cards, and a run beside an open board, proceed side
by side; two writers on the same card never do: the second is refused
("another gummi process is already driving this card"). The board holds the
lock of every session whose agent is running the same way, and of every
session whose workflow run it conducts, for as long as that run is open or
queued; it takes the workspace's one exclusive lock only against a second
board. A board open beside a headless run shows the session as driven
elsewhere and follows it.

A process conducts only the workflow runs of the sessions whose lock it
holds. `run` and `resume` conduct their own session's run and no other: a
run another session has open, which a board or another headless run is
conducting, is left to it — neither parked nor ticked.

Cards of the retired workflow kinds (FD, BG, RS) stay readable — `status`,
`spec`, `diff`, `merge` of one already verified — but nothing drives them:
`resume` on one is refused.

## Watching a run

`run` and `resume` stream NDJSON on stdout, and mirror it to
`.gummi/state/events.jsonl`:

```
{"event":"created","id":"FF-012","branch":"…","envelope":500,"always_allow":true}
{"event":"reply","id":"FF-012","text":"…"}                  each finished reply of the session's agent
{"event":"workflow","id":"FF-012","run":1,"recipe":"deliver","spec":1,"state":"running","spent":0,"steps":6}
{"event":"crew","id":"FF-012","crew":1,"state":"running","nodes":6}
{"event":"workflow","id":"FF-012","run":1,"recipe":"deliver","spec":1,"state":"done","spent":184.2,"steps":6,"report":"…"}
{"event":"crew","id":"FF-012","crew":1,"state":"finished","nodes":6,"report":"…"}
{"event":"done","id":"FF-012","branch":"…","spent_credits":212.5}
```

A `workflow` line is written whenever one of the session's runs changes
state: `proposed`, `queued` (allowed, behind the open run), `running`,
`paused`, and its ending — `done`, `partial`, `stopped` or `denied`. `spent`
is what its steps have spent so far, in credits; a run estimates nothing. The
report of an ended run rides its line, and the same report reaches the
session as a turn; the driver is not done until that turn has been answered.
`--verbose` adds one `activity` line per tool call of the session's agent,
run notes included.

`spent_credits` on the `done` and `exhausted` lines is what the session's
envelope has paid for: the session's own turns and every step of its
workflow runs, since they all draw from one pool (DESIGN §24.7a).

Each `workflow` line is followed by the `crew` line that crews wrote, for one
release (DESIGN §25.11): `crew` is the run's number, `nodes` its recipe's
step count (a run has no budget of its own, so the crew's `budget` is gone), and the state is the run's, with `done`
read as `finished`, `denied` as `declined` and `queued` as `proposed`. Read
`workflow`; the `crew` line goes away.

To measure where a run's spend went rather than watch it, `gummi status
<id> --json` carries `stage_spend`: one row per (stage, role) with the
model, the credits, and input/cached/output tokens.

## Exit statuses

Every `run` and `resume` ends on a typed exit the caller branches on:

| exit | status | last event | caller action |
|---|---|---|---|
| `0` | `done` | `done` | the session is idle with no workflow run open or queued. Report its branch, or send it a next turn with `resume <id> "<message>"` |
| `2` | `needs_you` | `question` | the session asked, or its workflow run has a decision, or a step asked, or the run is paused — see below |
| `5` | `exhausted` | `exhausted` | envelope dry. `resume --envelope N` with a higher number |
| `6` | `timeout` | `timeout` | nothing happened for the `--timeout` window. Report; resumable |
| `1` | `error` | `error` | setup or agent failure. `resumable` on the event says whether a session was left to resume |

The codes the retired workflow used for `blocked` (3) and `escalation` (4)
are not reused.

A `question` event's `kind` says whose question it is:

| kind | what stopped | how to continue |
|---|---|---|
| `ask` | the session's own `ask_user` | `resume <id> --answer "<text>"`: one of `options`, or your own words |
| `workflow_proposal` | the session proposed a run, and its runs are not always allowed (a session `gummi run` minted always allows them, so this is one minted elsewhere) | `--answer "Allow"`, `"Always allow"` (this run and every later one of the session, the envelope the only cap) or `"Deny"`; other words go to the session as feedback, and the run does not start |
| `workflow_plan` | a planner step revised the run's remaining steps — always a person's call (`step` names it) | `--answer "Allow the revised plan"` or `"Keep the plan"`; other words go to the planner step |
| `workflow_max` | a `repeat` reached its `max` without its `until`, and its `onMax` is `pause` (`step` names it) | `--answer "Run one more iteration"`, `"Continue"` or `"Stop the run"` |
| `workflow_unjudged` | a `repeat` could not judge its `until` after an iteration — the artifact its verdict is read from was never written, or a check ran no tests (`step` names the iteration) | `--answer "Run one more iteration"` (not counted against `max`), `"Continue"` or `"Stop the run"` |
| `workflow_budget` | the session's pool is spent and the run paused, its work kept | `--envelope <dollars>` (the `next` line suggests one) tops up and carries the run on; or `--answer "Top up the session"` (by the amount it names: as much again as the envelope), another amount to top up by with its unit — `300cr` in credits or `$3` in dollars — or `"Stop the run"` |
| `workflow_ask` | a step's own `ask_user` (`step` names it); the step waits | `--answer "<text>"`: one of `options`, or your own words |
| `workflow_paused` | the session's run is paused (a person paused it, or the process running it stopped) | a bare `resume <id>` resumes it; `--answer` is refused, since nothing asks anything |

A run's decision may also be answered by an option's number (`--answer 1`),
on every kind of decision, and an option's label in any case. A number is
always an option, never an amount: a bare number past the options is
refused, and an amount on `workflow_budget` is spelt with its unit (`300cr`,
`$3`). An answer is to the decision the resume read: one the run has
replaced since is refused, saying what the run asks now. Every `workflow_*` question also carries
`run` (the run's number) and, beside it for one release, the kind a crew's
stop of the same sort had as `crew_kind` (`crew_plan`, `crew_widen`,
`crew_question`, `crew_budget`, `crew_paused`), with `crew` and
`node` repeating `run` and `step`.

Every resumable stop carries `next`: the literal `gummi resume …` command for
it, with a `<placeholder>` where a value is yours to supply. `exhausted` and
`timeout` also carry `preconditions.check_running`, a shell probe that warns
when the card's recorded gummi pid is still alive — a wrapper killed under a
run can leave gummi working as an orphan, and a retry then fights the lock.

`gummi doctor` has its own exit status, outside this table: **7** while any
check is failing, 0 once the workspace is ready. It applies to `--json` too,
so a setup step can branch on the exit code instead of parsing `.ready`.

## Resuming

`gummi resume <id>` picks the session back up in a fresh process: the
session's conversation, its open question and its workflow runs come back
from the store. Then:

- `--answer "<text>"` answers the open question — the session's own
  (through the same round trip the board's picker rides, and the card's
  history records who answered), or else the run's open decision, or else a
  step's question. With nothing open it is refused before anything moves —
  a paused run included, which a bare `resume` runs again. A run decision
  answered on a run the process before left running carries the run on,
  unless something else still holds it (the money, or another decision).
- `"<message>"` — a second argument — is the session's next turn. Refused
  while a question is open.
- `--envelope N` raises the session's envelope before anything else, which
  carries on a run paused on a spent budget. It never lowers it.
  With no message, the session is told to carry on where it stopped.
- with none of these, it carries on: a run the process before left running
  (which comes back paused: nothing resumes itself, DESIGN §25.7) or a
  person paused is resumed — the person running `resume` is the one who
  may — and the driver waits on the session and its run as before. A run
  paused because the money ran out waits for `--envelope` instead.

`--timeout` and `--verbose` work as they do on `run`.

## Landing

A run never lands its session. The landing commit is a person's decision,
so the headless way to make it is explicit:

```sh
gummi merge FF-042 -m "feat(export): add a --format=json flag"
gummi merge FF-042 -m - <<'MSG'     # or read the message from stdin
feat(export): add a --format=json flag

The flag writes NDJSON to stdout instead of the table layout.
MSG
```

`merge` refuses a session whose workflow run is still open or whose diff has
unresolved comments (and a retired-workflow card that is not at a verified
branch), takes no other input, and is stricter than the board's dialog: the message must be a
Conventional Commits `type(scope): summary` with no diff dump and no agent
attribution, or the command refuses before touching git. On success it
emits a `merged` event with the landed sha and moves the card to `done`. The event's `method` is `squash` or `merge`. With `--no-squash` the `-m` is optional: the merge commit takes git's own merge message, and a `-m` given is validated and used verbatim.

Closing a card takes its worktree and branch with it. A branch is a piece
of work at a point in time, and a closed card has no further use for one,
so after `merge` (and after `handoff`, below) gummi removes both and emits
the `cleaned` event `clean` does. `--keep-branch` keeps them. A card with
uncommitted work and one in a stack are left
standing either way; `gummi clean` is there for them afterwards.

### Ending a card without landing it

Landing is one of three endings, not the only one. When the branch is
yours to take — you will push it, open the PR by hand, cherry-pick two
commits out of it, or simply keep it — `handoff` closes the card without
landing anything:

```sh
gummi handoff FF-042
{"event":"handed off","id":"FF-042","branch":"feat/json-export"}
```

It stamps `handed_off` and closes the card with the landing waived and
**every other floor intact**: an open diff annotation still refuses it, and so
does a workflow run open or queued on the session — a session is not closed
under its run, as it is not under its agent mid-turn; let the run finish or
stop it first. A run still only proposed is withdrawn. A session's loose work is not committed for it — commit what you
mean to keep first (`gummi commit`).

What happens to the branch depends on whether it is the only copy of the
work. A branch that was never pushed is: the hand-off leaves it and its
worktree exactly where they are, and landing it after all stays available
(`gummi merge` accepts a handed-off card and retracts the stamp). A branch
already on a remote — pushed, or with its PR open — is not, and it goes
with the card like any other closed card's, unless `--keep-branch` is
given.

Before this verb the only way to say "I'll take it from here" was to
delete the card, which destroys the branch.

`clean <id>` is the board's `c` key: it removes a card's worktree and
branch and keeps the card as a done entry. It takes a card whose branch
landed, or one that is closed, and refuses anything carrying uncommitted
work. A closed card's branch is deleted when its work exists somewhere
else: it landed, gummi recorded a landing for it (which still counts after
the base was reset or rebased past that commit), a remote has it, or it
never had a commit of its own. A branch that is none of those is the only
copy of its work: `clean` removes the worktree, keeps the branch, and the
`cleaned` event carries `"branch_kept": true`.

`clean --all` is the headless counterpart of the TUI's close-out sweep
(`C`): one pass over every closed card that still holds a worktree or a
deletable branch, instead of naming them one at a time. It streams the same `cleaned`
event per card the single-id form does, and exits non-zero only if a card
it attempted failed.

`status --json` carries these terminal signals separately, and a poller
must not conflate them:

| field | means |
|---|---|
| `stage: "done"` | the card is **closed**. It does not mean anything merged |
| `ending` | how it closed: `landed` or `handed_off`; absent while the card is open |
| `branch_state: "landed"` | the branch is on the trunk — the field to read for "did this merge" |

`verified` on the same payload belongs to cards of the retired workflow: a
session never carries it.

## Landing through a PR

Some repos land through a PR on GitHub instead of `gummi merge`. Nothing
gummi drives writes to GitHub on that route: `run` and `resume` only
name and read the PR. A person can open and update it from their own
terminal with `gummi pr create` and `gummi push` (each shows what it
will do and asks first), or do it by hand. The loop is four commands:

```sh
gummi pr link FF-042 --auto                          # or a URL / number instead of --auto
gummi pr comments FF-042 --ingest                    # unresolved review threads become diff annotations
gummi resume FF-042 "address the review comments on the diff"
git push                                             # push the fix onto the open PR
```

What you do before the first push depends on the repo's merge setting:

| merge method | before you push |
|---|---|
| squash merge | nothing; GitHub collapses the branch to one commit |
| merge commit / rebase merge | `gummi squash <id> -m <message\|->` first, so the branch lands as one commit either way. Later fix rounds may keep their own commits or `squash` again, as long as no review thread is open |

`squash` refuses while the worktree has uncommitted changes, because
folding them silently into the collapsed commit would hide what changed.
Commit them first, then squash:

```sh
gummi commit FF-042 -m "fix(export): tighten the empty-array case"
gummi squash FF-042 -m "feat(export): add a --format=json flag"
```

`commit` commits exactly the card's own uncommitted worktree changes onto
its own branch with your message. It touches no PR, remote or main
checkout, moves no stage, and has no precondition. A clean worktree is a
no-op, reported as such.

## The calling-agent skill

gummi generates its own skill documenting this loop, and installs it where
Claude Code, GitHub Copilot CLI and opencode read it:

```sh
gummi skill install          # project scope: .claude/skills
gummi doctor                 # then check backend, auth and envelope are ready
```

The skill is a **bundle**: `SKILL.md`, the common path an agent reads on
every invocation, plus `references/` files it points at —
`references/setup.md` (first-run readiness) and `references/resume.md`
(choosing a resume form, and the orphan check before a retry). Splitting it is what keeps it cheap:
an agent driving one session reads SKILL.md and nothing else.
`gummi skill show` prints SKILL.md; `gummi skill show resume` prints one
reference.

A project-scope install writes `.claude/skills/gummi/` for Claude, Copilot
and opencode. `--scope user` writes
to each detected agent's home instead, `--agent` targets one, `--dry-run`
prints what would be written, and `--check` fails if any target is absent or
drifted. The command grammar and exit table are generated from the same
cobra tree that parses a real command line, so the skill cannot document a
flag the binary lacks — or miss one it has. The frontmatter is
version-stamped and its hash covers the whole bundle, so `install` and
`list` detect a stale or edited file — a reference file included — and
refuse to overwrite it without `--force`.

`gummi doctor` is the readiness check the skill's first-run setup runs
(`--json` for a machine-readable checklist, `--deep` to probe each role's
backend). It reports and never repairs: a backend needing login is
surfaced as the exact command for a human to run. Provider config lives in
each backend's native store, never in `profiles.yaml`.
