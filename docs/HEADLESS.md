# Driving gummi headlessly

The board is one way in. The other is the non-interactive driver: the same
engine and the same quality floor, with nobody at the keyboard. This page is
the full reference for that path. The README carries the short version, and
`gummi skill show` prints the loop as a calling agent reads it.

Headless verbs change *who approves a gate*, never *whether* review and
verify run. `run` and `resume` never merge. They stop at a
verified branch, and landing is the separate `merge` verb.

## Starting a card

```sh
gummi run --envelope 5 "Add a --format=json flag to the export command"
gummi run --envelope 5 --gate-approval autopilot "..."
```

Two things must be true before any work begins, and both fail loud:

- an envelope is named, in dollars (`--envelope 5`, `$12.50`, or
  `GUMMI_ENVELOPE`). The board's creation form prefills $20 you can edit; an unattended run has
  no one to read a default, so it must name one.
- an agent backend is configured. `gummi doctor` tells you which.

`--gate-approval` takes one of two modes. `attended` (the default) stops at
the design gate and hands the decision back through `resume`. `autopilot`
crosses its own gates and runs to a verified branch. The mode is saved on
the card; `resume` keeps it unless you pass the flag again. The retired
spellings `off`, `gates`, `caller`, `auto` and `full` are still accepted
and mapped.

Other `run` flags:

| flag | purpose |
|---|---|
| `--until plan` | stop cleanly before implementation, for a human design review. `plan` is the only stop |
| `--autonomous` | take the agent's recommended answer instead of stopping on a question. The card's history marks the answer as unattended |
| `--acceptance <file\|->` | seed the spec's verification plan |
| `--ref <id>` | your own tracker id, echoed in the stream. `status` and `resume` accept it in place of the card id |
| `--repo <name>` | which managed repository to create the card in. Required when `repos:` is configured |
| `--profile <name>` | the profile mapping roles to models |
| `--stage-timeout <dur>` | per-stage inactivity timeout, 0 disables |
| `--verbose` | add per-tool-call lines to the stream |

Wherever a verb takes a card as `<id>`, its number alone works too:
`gummi status 42` is `gummi status FD-042`. The stream and `--json` always
carry the full id. One flag still wants the id itself:
`schedule --heartbeat`.

## Verbs

| command | purpose |
|---|---|
| `gummi run [flags] "<description>"` | create and drive one feature to a verified branch |
| `gummi resume <id\|ref> [decision]` | apply a decision and drive on |
| `gummi resume <id\|ref> --say "<line>"` | read a line the way the card page would and report what it would do, as a `say` event, without acting |
| `gummi status <id\|ref> [--json]` | stage, blockers, spend, branch state |
| `gummi status <id\|ref> --stats [--json]` | how the card ran: each pass and what it cost, the share that was rework, the share of its life spent waiting on a person |
| `gummi watch <id\|ref> [--json] [--wait] [--once]` | follow the live agent stream of a card another gummi is driving |
| `gummi spec <id\|ref>` | the current spec or report markdown |
| `gummi diff <id\|ref>` | the worktree diff against main |
| `gummi verify <id\|ref>` | re-run the checks on a verified branch and finalize its card |
| `gummi merge <id\|ref> -m <message\|-> [--no-squash] [--keep-branch]` | land a verified branch as one squash commit, or with `--no-squash` as a merge commit keeping its commits, where `-m` is optional and git's own merge message is used |
| `gummi squash <id\|ref> -m <message\|->` | collapse a card's branch to one commit in place |
| `gummi commit <id\|ref> -m <message\|->` | commit a card's own uncommitted worktree changes onto its branch |
| `gummi handoff <id\|ref> [--keep-branch]` | close a verified card without landing it; its branch is kept while it is the only copy of the work |
| `gummi clean <id\|ref>` \| `gummi clean --all` | remove a landed or closed card's worktree and branch (an adopted card keeps its branch, and so does a closed card whose branch is the only copy of its work); `--all` does this for every closed card in one pass instead of naming one |
| `gummi pr link\|unlink\|status\|comments <id> [flags]` | link a card to a PR you opened, or read its status and review comments |
| `gummi push <id>`, `gummi pr create\|update\|ready\|draft <id> [flags]` | a person publishes a card: push its branch, open or update its PR, mark it ready or draft. Prints the facts and commands and asks at the terminal; without one, `--yes=<fingerprint>` of facts already printed. Refused inside a gummi-spawned session (DESIGN §22) |
| `gummi deps add\|rm <dependent> <depends-on>`, `gummi deps list <id>` | dependency edges between cards |
| `gummi init` | create and seed the `.gummi` workspace without opening the board |
| `gummi doctor [--json] [--deep]` | readiness: repo, backend, auth, profile, envelope, lock, per-role reach |
| `gummi skill show\|install\|list` | the calling-agent skill |

`status`, `watch`, `spec` and `diff` take no lock, so you can inspect a card
while a run is live. Everything that drives or writes a card (`run`,
`resume`, `verify`, `merge`, `squash`, `commit`, `clean`) holds
that card's own lock for the length of the command — not a lock on the
whole `.gummi` workspace. Headless runs on different cards, and a run
beside an open board, proceed side by side; two writers on the same card
never do: the second is refused ("another gummi process is already driving
this card"). The board holds the
lock of every card it is driving the same way, and takes the workspace's
one exclusive lock only against a second board.

## Watching a run

`run` and `resume` stream NDJSON milestones on
stdout. `--verbose` adds one `activity` line per tool call. The line worth
knowing is `stage`, whose `result` names what the card is doing:

```
{"event":"stage","stage":"plan"}                        the stage's own session
{"event":"stage","stage":"plan","result":"critiquing"}  the adversarial read
{"event":"stage","stage":"plan","result":"changes"}     it found something blocking
{"event":"stage","stage":"plan","round":1,"result":"replanning"}
{"event":"stage","stage":"plan","result":"pass"}
{"event":"stage","stage":"plan","result":"discovering checks"}
{"event":"stage","stage":"plan","result":"baselining checks"}
{"event":"gate","from":"plan","to":"implement",...}
{"event":"stage","stage":"verify","result":"drafting the landing message"}
```

The last two are the one-shot passes that run at the approval gate — the
scribe survey that writes the repo's commands into the spec's
`gummi-checks` block, and the run of those commands that records which
were already failing. They are model-and-shell work that can take minutes
on a large repository, which is why they are on the stream: between the
plan's verdict and the gate crossing, they are the only thing happening.
They carry the stage the card is crossing **from**, because that is the
gate they belong to — the card's own stage field has already advanced by
the time they start.

The last line is the same kind of pass at the other end of the drive.
When a run stops on a verified branch it composes the card's squash-merge
landing message and stores it, so the person who comes back to land the
card opens a merge dialog that already holds one instead of waiting
~60s for it — the draft is stamped with the branch tip it describes, and
a branch that moves afterwards is drafted live at the landing as before.
It is skipped, silently, for a card whose message is written by something
else: a card linked to a PR, a research card, and one already handed
off. The run stops
at its verified branch either way — a failed draft costs the landing
nothing but the wait it was meant to save.

The survey is remembered per repository, not per card: a second card in
the same repo reuses the first card's answer and skips the session
entirely, until one of the files that decides the answer changes (the
Makefile or task file, the CI workflows, the dependency manifest, the
lint config, or the repo's AGENTS.md/CLAUDE.md). Delete
`.gummi/checks-cache.json` to force a fresh survey.

A finished card's receipt carries three numbers worth reading together:

```
{"event":"verified","spent_credits":661.4,
 "review_rounds":3,            the critiques THIS invocation ran
 "corrective_rounds":1,        the CARD's cumulative rework, across processes
 "unproven_files":["lxd/images.go"]}
```

`unproven_files` is verify's own declaration of the files this branch
changed that no check that ran exercised — a tree the local toolchain
cannot build, a suite only CI runs. It does not block the card; it is how
a caller tells a branch every check covered from one where a whole
subtree was never compiled. `gummi status --json` carries the same list
(with each file's reason) beside `excused_checks`, and both qualify
`verified` in the same way.

To measure where a run's spend went rather than watch it, `gummi status
<id> --json` carries `stage_spend`: one row per (stage, role) with the
model, the credits, and input/cached/output tokens. It is the supported
way to answer "what did this card cost, and where" — the `spend` field
above it is only the total.

## Exit statuses

Every `run` and `resume` ends on a typed exit the caller
branches on:

| exit | status | caller action |
|---|---|---|
| `0` | `verified` | verified branch ready. Report it and stop |
| `0` | `stopped` | `--until` reached its stop. `resume --approve` to continue |
| `0` | `said` | `--say` reported a reading and acted on nothing |
| `2` | `question` | a delegated question or a design gate. `resume --answer`, `--approve` or `--request-changes` |
| `3` | `blocked` | open `%%` or diff threads block a gate (resolve them, or `resume --request-changes`), an unmet dependency blocks the coding stage (`blocking_deps` on the event: wait for it to land, or `gummi deps rm`), or the plan's own promises are unmet at the verify→done gate (`reason` names them: an invariant verify never answered or answered fail, or a golden whose quoted input appears nowhere on the branch) |
| `4` | `escalation` | a rerun or critique cap, or an unclear verdict. Report to a human; resumable |
| `5` | `exhausted` | envelope dry. `resume --envelope N` with a higher number |
| `6` | `timeout` | a stage went quiet. Report; resumable |
| `1` | `error` | setup or agent failure. Nothing partial landed |

`gummi doctor` has its own exit status, outside this table: **7** while any
check is failing, 0 once the workspace is ready. It applies to `--json` too,
so a setup step can branch on the exit code instead of parsing `.ready`. The
code sits above the drive statuses on purpose — a workspace that is not ready
is not a run that asked a question.

## Resuming

`gummi resume` carries one decision flag at a time:

- `--answer "<text>"` resolves a delegated `ask_user` question. The answer
  rides the same round trip the board's picker rides, and the card's
  history records who answered and which option was chosen. When more
  than one decision is open, the newest one is resolved.
- `--approve` / `--request-changes "<note>"` decide a design gate. Passing
  `--answer` at a gate, or `--approve` at a question, is refused with a
  usage error naming the verb this stop actually takes.
- `--bounce [--note "<why>"]` rewinds one rerun edge: a verify failure back
  to implement, an implement-stage card back to plan. It is the board's
  `b` key.
- `--envelope N` raises the envelope before resuming. It never lowers it.
- `--say "<line>"` reads a line the way the card page reads typed text and
  reports the routing as a `say` event without acting. Use it to see what
  a sentence would do before you commit to a verb.


## Landing

A run stops at a verified branch on purpose. The landing commit is a
review decision, so the headless way to make it is explicit:

```sh
gummi merge FD-042 -m "feat(export): add a --format=json flag"
gummi merge FD-042 -m - <<'MSG'     # or read the message from stdin
feat(export): add a --format=json flag

The flag writes NDJSON to stdout instead of the table layout.
MSG
```

`merge` requires the card to be at a verified branch, takes no other
input, and is stricter than the board's dialog: the message must be a
Conventional Commits `type(scope): summary` with no diff dump and no agent
attribution, or the command refuses before touching git. On success it
emits a `merged` event with the landed sha and moves the card to `done`. The event's `method` is `squash` or `merge`; `--no-squash` is refused for a card a session delegated, which lands on its parent as a squash. With `--no-squash` the `-m` is optional: the merge commit takes git's own merge message, and a `-m` given is validated and used verbatim.

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
gummi handoff FD-042
{"event":"handed off","id":"FD-042","branch":"feat/json-export"}
```

It commits a final checkpoint first (the branch is the deliverable, so
loose work must not be left behind), stamps `handed_off`, and crosses the
same verify→done gate with the landing waived and **every other floor
intact**: open `%%` threads, open diff annotations, the omission gate and
the document floor all still refuse it.

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
| `verified: true` | the verify gate passed and the branch is ready to land — where a headless run stops, and what a CI caller polls for |
| `stage: "done"` | the card is **closed**. It does not mean anything merged |
| `ending` | how it closed: `landed` or `handed_off`; absent while the card is open |
| `branch_state: "landed"` | the branch is on the trunk — the field to read for "did this merge" |

After a headless run expect `verified:true` with no `ending` until you
merge or hand off.

The TUI names the same words on the board badge and in a finished
card's closing block.

`ending` replaced the `done` and `handed_off` booleans, which took two
fields to name one fact.

`rounds` on the same payload is a **live counter, not a history**. Each
loop resets its own count when it completes, so a finished card that took
three plan rounds still reads `{"plan": 0, "review": 0, "corrective": 0}`.
The tally of critique passes is `review_rounds` on the terminal `verified`
event. A poller reading `rounds` as "how much rework did this take" will
read zero every time.

## Landing through a PR

Some repos land through a PR on GitHub instead of `gummi merge`. Nothing
gummi drives writes to GitHub on that route: `run` and `resume` only
name and read the PR. A person can open and update it from their own
terminal with `gummi pr create` and `gummi push` (each shows what it
will do and asks first), or do it by hand. The loop is four commands:

```sh
gummi pr link FD-042 --auto                          # or a URL / number instead of --auto
gummi pr comments FD-042 --ingest                    # unresolved review threads become diff annotations
gummi resume FD-042 --bounce --note "address review" # rewinds to fix the annotated lines
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
gummi commit FD-042 -m "fix(export): tighten the empty-array case"
gummi squash FD-042 -m "feat(export): add a --format=json flag"
```

`commit` commits exactly the card's own uncommitted worktree changes onto
its own branch with your message. It touches no PR, remote or main
checkout, moves no stage, and has no precondition. A clean worktree is a
no-op, reported as such.

## Dependencies

`gummi deps add <dependent> <depends-on>` records that one card needs
another (`rm` and `list` remove and read the edges). A dependency counts
as met only when the target reaches `done`, meaning verified *and*
landed. Anything short of that blocks the dependent card's entry into
implement with a `blocked` exit naming the outstanding cards. The board
shows the same edges through the `p` key's dependency picker.

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
(choosing a resume verb, and the orphan check before a retry). Splitting it is what keeps it cheap:
an agent shipping one card reads SKILL.md and nothing else.
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
