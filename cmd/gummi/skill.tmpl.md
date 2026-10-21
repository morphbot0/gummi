# Using gummi to get work done in a session

gummi is a meta-harness for coding agents. `gummi run` starts a **session** — a
coding agent on a git branch and worktree of its own — from your prompt, and
drives it headlessly. When the work divides, the session can start a **crew**:
a planner turns the session's spec into a plan of specialist nodes (scouts, a
contractor, implementers, testers, a scribe), each checked and reviewed on a
branch of the crew's own, and the crew's report comes back to the session.
Running `gummi run` pre-authorises a crew up to the envelope you give it; a
crew plan over that, a widening of an approved plan, or a question gummi will
not answer for a person stops the run and comes back to you. gummi never
merges on its own.

## When to use gummi (vs. editing directly)

Use gummi for **work that deserves its own branch and an agent's full
attention** — a feature, a bug fix, an investigation — where you want an
isolated branch a human can read before it lands.

Edit files directly for **trivia**: a typo, a one-line config tweak, a rename —
anything where a separate session is pure overhead.

## Before the first run

```
gummi doctor --json
```

It returns a readiness checklist and exits **7** while anything is failing, 0
once the workspace is ready. If it is not ready, fix it with
`references/setup.md` — backend, profile, auth and envelope, each with what to
write and what to hand the human. Skip all of that once it reports
`ready: true`; you never need it again in this repo.

## Command grammar

```
{{.Grammar}}
```

`status`, `watch`, `spec`, and `diff` are **read-only** — they take no lock, so
you can inspect a session while a run is live. `status` answers *where* a card
is (a store snapshot); `gummi watch <id>` follows what it is doing *right now*
— the agent's own transcript, tool calls, and state changes, streamed off the
driving process's live file. `watch --json` emits that stream as NDJSON.

## The loop

Each `gummi run` / `gummi resume` streams NDJSON on stdout — `created`,
`reply` (each finished reply of the session's agent), `crew` (a crew changing
state), and one terminal line — and exits with a **typed status**. Branch on
the exit code:

{{.ExitTable}}

**Capture that exit code directly — never through a pipe.** `gummi run … | tee
run.log` (or `| head`, `| grep`) reports the *filter's* exit status, not
gummi's, so a `needs_you`/`exhausted`/`error` looks like success. Redirect to
a file and read `$?` on the next line:

```
gummi run --envelope N "<prompt>" > run.log; ec=$?
```

(or `set -o pipefail` if you must pipe). The exit code is also redundant with
the stream: the **last NDJSON line's `event`** (`done`, `question`,
`exhausted`, `timeout`, `error`) is the terminal status.

Every resumable stop carries a **`next`** field: the literal `gummi resume …`
command for that stop (a value you must supply appears as a `<placeholder>`,
e.g. `--answer "<answer>"`; `exhausted` pre-fills a raised `--envelope`).
**Prefer running `next` verbatim over assembling the command yourself.**

The loop you run:

1. `gummi run --envelope N "<prompt>"` (add `--ref <id>` to correlate with your
   own tracker).
2. Read the exit code:
   - **done (0)** — the session is idle with no crew open. Its replies are on
     the stream; its work is on the branch in the final `done` line, not
     merged. Report it and stop, or send the session its next turn with
     `gummi resume <id> "<message>"`.
   - **needs_you (2)** — the `question` event says whose question it is
     (`kind`): the session's own (`ask`), a crew plan over the
     pre-authorisation (`crew_plan`), a widening of an approved crew plan
     (`crew_widen`), a crew node's question the planner would not answer
     (`crew_question`), or a paused crew (`crew_paused`). Answer from your task
     context first; read the session's spec (`gummi spec <id>`) or diff
     (`gummi diff <id>`) if unsure; use `recommended` only as a tiebreaker.
     Then `gummi resume <id> --answer "<option-or-your-words>"`. A crew
     decision's options are fixed phrases (`Run the crew`, `Keep it a
     session`, `Widen the plan`, `Keep the scope: write it into Limits`);
     other words go to the crew's planner as a note. A paused crew resumes
     with a bare `gummi resume <id>`. A widening is the person's call: ask the
     human unless your task already settles it.
   - **exhausted (5) / timeout (6)** — stop and report to the human. Both are
     durable and resumable, and `next` carries the command. Run the
     `preconditions.check_running` probe on the event before retrying —
     `references/resume.md` says why.
   - **error (1)** — a setup/agent failure. A failure before the session
     existed landed nothing (report it); one after leaves a session that a
     `gummi resume <id>` may carry on — the error event's `resumable` says
     which.
3. After a `resume`, read the new exit code and repeat until `done`.

## Worktrees

Each session is checked out in its **own linked git worktree** under
`.gummi/worktrees/FF-NNN`. Its branch is *already checked out there*, so a
plain `git checkout` of it in the main repo fails (`exit 128`, "already checked
out"). To build, test, or inspect the work outside gummi, `cd` into its
worktree.

## Landing and cleanup

`run`/`resume` never merge. Landing is a separate, explicit verb: `gummi merge
<id> -m "<message>"` (or `-m -` to read the message from stdin) squash-merges
the branch into main — only with a Conventional Commits `type(scope): summary`
message with no diff dump or agent attribution, and refused while the session
has a crew open or unresolved diff comments. Draft that message from the
session's work and the branch's own commits, and have the human confirm it
before you run `gummi merge` — don't invent a message and merge unattended.
`gummi clean <id>` removes a landed card's worktree and branch afterward. Both
hold the same per-card lock as `run`/`resume`.

`gummi commit <id> -m "<message>"` commits exactly a session's own uncommitted
worktree changes onto its own branch, with the same message rules as
`merge`/`squash`. It has no PR-linked precondition, unlike `merge` (which
refuses a PR-linked card outright) and `squash` (which refuses a dirty worktree
outright): use `commit` first to turn stray worktree changes into a commit,
then run `squash` on a PR-linked card that has them. A clean worktree is a
no-op, not an error.

`gummi log <id>` lists a card's own commits, oldest first (`--json` for the
SHAs). `gummi rewrite <id> --plan <file|->` rewords or squashes them in place:
the plan lists every commit, oldest first, in contiguous groups, each group
becoming one commit with the message given. Reordering and dropping cannot be
expressed, so the branch's content never changes; `--dry-run` shows the result
first. `--sign` (alone, or with a plan) makes every commit from the first
unsigned one up again, signed, where commits are signed at all. It refuses while an agent holds the card, on an adopted branch, and on
commits already pushed unless `--allow-pushed` — then prints the force push,
which it never runs. Like `squash`, rewriting history is the human's call: ask
before you run it.

## What gummi guarantees

- **gummi never merges on its own.** Landing (a PR, a push, a squash-merge)
  stays with a person.
- **A crew never runs past what was authorised.** A plan over the
  pre-authorisation and every widening wait for an answer; a question a crew
  node asks is answered from the frozen spec or comes to you.
- **It fails loud.** Anything a person would resolve — a question, an
  exhausted envelope, a stalled session — becomes a typed non-zero exit and a
  durable, resumable session, never a silent auto-proceed.

Your job: drive the loop, answer what comes to you, and report the branch
upward.

## References

Read these only when you need them; SKILL.md above is the whole common path.

- `references/setup.md` — first-run readiness: backend, profile, auth, envelope.
- `references/resume.md` — choosing a `resume` form yourself, and the
  orphan-process check to run before any retry.
