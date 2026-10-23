# gummi: picking the right `resume`

SKILL.md's loop covers the common path: every resumable stop carries a
**`next`** field holding the literal `gummi resume …` command for that stop.
Running `next` verbatim is right almost every time. This file is for when you
need to choose yourself.

## Match the form to why the run stopped

- `--answer "<text>"` — answers a **`question`** event: the session's own
  `ask_user` (`kind: ask`), its workflow run's open decision
  (`workflow_proposal`, `workflow_plan`, `workflow_max`, `workflow_runaway`,
  `workflow_budget`), or a step's own question (`workflow_ask`). For a run
  decision, one of the event's `options` (or its number) decides it; other
  words to a proposal reach the session as feedback, and to a revised plan
  the planner step. A number is always an option: a top-up of another
  amount is spelt with its unit, `300cr` or `$3`. With nothing open — a
  `workflow_paused` stop included — `--answer` is refused.
- `"<message>"` (a second argument) — the session's next turn. Refused while a
  question is open: answer it with `--answer` instead.
- `--envelope <dollars>` — clears an **`exhausted`** stop; it must exceed the
  dry envelope (the `envelope` field on the event), and only raises, never
  lowers. It raises the workflow pre-authorisation to match, and with no message
  tells the session to carry on where it stopped.
- `--timeout <duration>` — retries a **`timeout`** with more room. The event
  carries `timeout_used`; if the backend was fine and just needed longer,
  double that rather than retrying at the same limit.
- **neither** — carries on: a paused run (`workflow_paused`) is resumed, a
  run left running by a process that died is resumed, and the run waits for
  the session and its workflow run as before. A bare `resume` is **not** how you answer
  a question: the question is still open, and the run stops on it again.

`--answer` and a message are mutually exclusive. `--envelope` and `--timeout`
compose with either, or stand alone.

## Before you retry: check `preconditions.check_running`

`exhausted` and `timeout` events carry a `preconditions.check_running` shell
one-liner — run it before following `next`.

gummi ignores SIGHUP so that a detached wrapper's death doesn't kill the model
turn; the flipside is that your wrapper dying (e.g. the harness killed it) can
leave gummi still churning as an orphan. A bare retry there fights the
exclusive lock and looks like a fresh failure.

`check_running` reads that card's own pid file under `.gummi/state/locks/` and
probes it with `kill -0`. If it prints "gummi still running as pid …", wait
until the pid is gone, then follow `next`:

- `gummi watch <id>` follows that run's live agent stream.
- `tail -f .gummi/state/events.jsonl` shows the run's events.

`gummi status <id> --json` also exposes this under `"running"` when you'd
rather branch on JSON than a shell probe.

## A session someone else is driving

**A session a person has open on the board is locked, too.** The board takes
the same per-card lock as `run`/`resume`/`merge`/`clean` while a session's
agent is running, so one of those verbs on it exits 1 with "another gummi
process is already driving this card". That is a wait-or-ask, not a fault to
repair: `gummi watch <id>` shows what it is doing. Independent cards are
unaffected — the lock is per card, never per workspace.
