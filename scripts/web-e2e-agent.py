#!/usr/bin/env python3
"""A deterministic scripted agent for gummi's browser end-to-end suite.

Speaks gummi's headless backend protocol (internal/agent/headless.go) on
stdio, so the engine, the worktrees, the specs, the checks and the diff are
all real -- only the model's words are pre-authored. Wire it in with:

    GUMMI_AGENT=headless GUMMI_AGENT_CMD=scripts/web-e2e-agent.py gummi ...

A test picks a scenario by putting a keyword in the card's title or
description (the agent reads it back out of the session's hints and the
artifact):

    (none)         plan writes a complete spec whose gummi-checks pass
                   (`go build ./...`, `go test -count=1 ./...`); implement adds a Go
                   file and its test and commits; every critique, review and
                   verify passes.
    [fail-check]   implement commits a test that passes on its first run and
                   fails on every run after (see FAIL_CHECK_TEST), so the
                   review's check run passes and verify's `go test` check
                   fails: verify reports fail on a real failing check.
    [fail-verify]  every check passes, but the verifier reports fail anyway
                   (the verification plan "does not hold").
    [ask]          the plan stage writes the problem and two approaches, then
                   puts the choice to the person with ask_user (two options,
                   changes_section "Chosen approach"), and finishes the spec
                   with whatever the answer was.
    [ask-where]    a session asks where the helper lives (two options) and
                   says back "Decided with the user: <answer>"; the
                   [ask-*] variants below do the same in a session.
    [ask-multi]    as [ask], but the question takes several answers
                   (multi_select), so a test can pick more than one.
    [ask-long]     as [ask], but the question is a multi-paragraph text of
                   about 1200 characters with three options, so a test can
                   prove a long question and every answer both stay in reach
                   on a phone as well as a desktop.
    [ask-gives-up] as [ask], but the agent's own tool call times out before
                   the person answers (GUMMI_E2E_ASK_TIMEOUT seconds, default
                   2): it says so and ends its turn with the question still
                   open, which is what a backend whose MCP client bounds a
                   tool call does to a question nobody answered in time.
    [ask-dies]     as [ask], but the agent process dies behind its question
                   (GUMMI_E2E_ASK_TIMEOUT seconds after asking, exit 3): the
                   backend crashed, was killed or lost its connection while
                   the person was still reading. A process started later
                   for the same card answers normally.
    [ask-spends]   as [ask], but the agent goes on spending while its
                   question is open (GUMMI_E2E_ASK_SPEND credits, default
                   5000), the way a model that polls or sleeps in its shell
                   waiting for the answer does: the card runs out of budget
                   behind its question.
    [slow]         every stage streams its reply in small text deltas with a
                    pause between them (GUMMI_E2E_SLOW_SECONDS, default 6s
                    per stage in total), so a test can watch a live card. An
                    interrupt frame stops the stream early.
    [fold]         the plan stage paces a tool call so a test can unfold its
                    activity row while it runs: `read` stays running for
                    GUMMI_E2E_FOLD_SECONDS (default 2.5s, real sleeps -- not
                    FAST beats), settles, pauses briefly, appends `grep`, then
                    asks its question (as [ask]). After the answer it writes
                    the chosen approach and holds for the same pause again
                    before finishing the spec, so a test can assert inside the
                    still-live session.
    [research]     nothing extra: a research card (RS-*) is recognised by its
                    kind. Its plan stage writes Questions/Constraints/Direction;
                    the keyword just makes a test's intent readable.

A rebase-resolve pass (the board's "let the agent resolve them") runs
the command its kickoff names; a conflicted path takes the card's side,
and an untracked file in the way is moved aside and folded back in.

An ingest pass (`gummi ingest`, the board's ingest dialog) proposes one
feature per `## ` heading of the source document; a heading that starts
`Unmapped:` is reported in the coverage map as a requirement no proposal
covers.

Any other message sent to a live session (a steer, a consult, a freeform
turn) gets a short acknowledgement quoting it; a freeform card's turn
also edits a file so its diff is non-empty. A freeform turn
containing [watch] starts a Monitor watch it leaves open, one containing
[ask] asks the person which way with ask_user and says back what they
answered, one containing [spec] writes the session's spec before its edit,
and one containing [tasks-done] completes its checklist.

A session turn containing [workflow] proposes a workflow run (DESIGN §25):
it starts a spec (spec_start), writes its Done when as two items -- one
checked by `test -f made.txt`, one judged -- and calls workflow_run with the
bundled deliver recipe, or the one [workflow=<name>] names. Each step is
answered by its role, read off its brief: a writing role (contractor,
implementer, tester, scribe) writes the run's artifacts its task names and,
for an implementer or tester, every file a `test -f` check of the spec looks
for; a reviewer writes {"verdict": "APPROVED"} into review.json where its
task names it, answers MET: for every done-when item where it is asked to
validate, and APPROVED otherwise; a scout reports; a planner keeps the plan.
The session acknowledges the run's report without touching its tree. A
[workflow] turn that also says [step-asks] marks its spec so, and the run's
implementer asks its person with ask_user (keep made.txt plain, or add a
header) before it writes, naming the answer in its output; an answer
that reaches a fresh process (the one that asked has ended) is the step's
too, read off the brief in its hints. A person's
message to a step after it ended is answered "Noted: <its first line>". A
message opening with [slow] is streamed slowly, so a test can interrupt it.

Environment:
    GUMMI_E2E_FAST=1          skip the small pacing beats (the suite sets it;
                              [slow] cards stay slow regardless)
    GUMMI_E2E_SLOW_SECONDS    total streaming time of one [slow] stage
    GUMMI_E2E_AGENT_LOG       append every frame in and out to this file
"""

import json
import os
import queue
import re
import subprocess
import sys
import threading
import time

FAST = os.environ.get("GUMMI_E2E_FAST") == "1"
SLOW_SECONDS = float(os.environ.get("GUMMI_E2E_SLOW_SECONDS") or "6")
ASK_TIMEOUT = float(os.environ.get("GUMMI_E2E_ASK_TIMEOUT") or "2")
ASK_SPEND = float(os.environ.get("GUMMI_E2E_ASK_SPEND") or "5000")
FOLD_PAUSE = float(os.environ.get("GUMMI_E2E_FOLD_SECONDS") or "2.5")
LOG = os.environ.get("GUMMI_E2E_AGENT_LOG")
CREDITS = 12  # per turn; small enough that no envelope in the suite runs dry


# --------------------------------------------------------------------------
# wire
# --------------------------------------------------------------------------

def log(direction, obj):
    if not LOG:
        return
    with open(LOG, "a", encoding="utf-8") as fh:
        fh.write("%s %d %s\n" % (direction, os.getpid(), json.dumps(obj)))


def emit(obj):
    log("OUT", obj)
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


# Frames from gummi arrive on a reader thread so a slow stream can notice an
# interrupt, and so a blocking tool call can wait for its resolve frame.
INBOX = queue.Queue()


def reader():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            frame = json.loads(line)
        except ValueError:
            continue
        log("IN", frame)
        INBOX.put(frame)
    INBOX.put(None)  # EOF


class Interrupted(Exception):
    pass


class Closed(Exception):
    pass


# Frames that arrived while a tool call was waiting for its resolve; the
# main loop handles them next.
DEFERRED = []


def next_frame():
    if DEFERRED:
        return DEFERRED.pop(0)
    frame = INBOX.get()
    if frame is None:
        raise Closed()
    return frame


def poll_interrupt():
    """Non-blocking: raise Interrupted if gummi asked us to stop."""
    while True:
        try:
            frame = INBOX.get_nowait()
        except queue.Empty:
            return
        if frame is None:
            raise Closed()
        if frame.get("type") == "interrupt":
            raise Interrupted()
        DEFERRED.append(frame)


class TimedOut(Exception):
    pass


def call_tool(name, args, timeout=None, spend=0):
    """Invoke a gummi client tool and block until gummi resolves it.

    ask_user resolves when the person answers; the other tools resolve at
    once. The result string is returned. With a timeout the call is given
    up on after that many seconds (TimedOut), the way a backend's own MCP
    client does; gummi is not told.
    """
    call_id = "%s-%d-%d" % (name, os.getpid(), int(time.time() * 1000))
    emit({"type": "ask", "id": call_id, "name": name, "ask": args})
    if spend:
        emit({"type": "usage", "credits": spend, "input": 1000, "output": 100})
    deadline = None if timeout is None else time.time() + timeout
    while True:
        try:
            frame = INBOX.get(timeout=None if deadline is None else max(0.0, deadline - time.time()))
        except queue.Empty:
            raise TimedOut()
        if frame is None:
            raise Closed()
        kind = frame.get("type")
        if kind == "resolve" and frame.get("id") == call_id:
            return frame.get("result") or ""
        if kind == "interrupt":
            raise Interrupted()
        DEFERRED.append(frame)


# --------------------------------------------------------------------------
# narration
# --------------------------------------------------------------------------

class Turn:
    """What one turn says and does, paced by the card's scenario."""

    def __init__(self, ctx):
        self.ctx = ctx
        self.slow = "[slow]" in ctx["keywords"]

    def beat(self, seconds=0.2):
        if self.slow:
            return  # a slow card paces its stream, not its tool calls
        if not FAST:
            time.sleep(seconds)
        poll_interrupt()

    def think(self, text):
        self.beat()
        emit({"type": "reasoning", "text": text})

    def tool(self, name, detail, call_id=None):
        self.beat()
        emit({"type": "tool", "name": name, "detail": detail, "id": call_id or ""})

    def tool_result(self, call_id, name, ok=True, result=""):
        emit({"type": "tool_result", "id": call_id, "name": name, "ok": ok, "result": result})

    def tasks(self, *items):
        emit({"type": "tasks", "tasks": [{"text": t, "status": st} for t, st in items]})

    def say(self, text):
        """A finished assistant message. A [slow] card streams it first."""
        if self.slow:
            words = text.split(" ")
            chunks = max(8, min(40, len(words)))
            step = max(1, len(words) // chunks)
            pause = SLOW_SECONDS / chunks
            for i in range(0, len(words), step):
                emit({"type": "text", "text": " ".join(words[i:i + step]) + " "})
                time.sleep(pause)
                poll_interrupt()
        else:
            self.beat()
        emit({"type": "message", "text": text})

    def usage(self, credits=CREDITS):
        emit({"type": "usage", "credits": credits, "input": 4000, "output": 300,
              "model": self.ctx["model"]})


# --------------------------------------------------------------------------
# the artifact (spec / research document)
# --------------------------------------------------------------------------

def read_doc(path):
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except OSError:
        return ""


def set_section(doc, title, body):
    """Replace the body of '## <title>', appending the section if missing."""
    lines = doc.split("\n")
    head = "## " + title
    start = next((i for i, ln in enumerate(lines) if ln.strip() == head), None)
    if start is None:
        return doc.rstrip("\n") + "\n\n" + head + "\n\n" + body.strip() + "\n"
    end = next((i for i in range(start + 1, len(lines)) if lines[i].startswith("## ")), len(lines))
    return "\n".join(lines[:start] + [head, ""] + body.strip().split("\n") + [""] + lines[end:])


def write_sections(turn, sections):
    path = turn.ctx["spec"]
    doc = read_doc(path)
    if not doc:
        return
    for title, body in sections:
        doc = set_section(doc, title, body)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(doc)
    turn.tool("edit", os.path.basename(path))


# --------------------------------------------------------------------------
# the tiny Go module the suite's workspace holds
# --------------------------------------------------------------------------

def ident(ctx):
    """A Go identifier unique to the card: FD-003 -> Fd003."""
    card = ctx["card"] or "Card"
    return re.sub(r"[^A-Za-z0-9]", "", card.title())


def git(workdir, *args):
    subprocess.run(["git", "-C", workdir] + list(args), check=False,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def write_file(workdir, name, text):
    with open(os.path.join(workdir, name), "w", encoding="utf-8") as fh:
        fh.write(text)


def go_package(workdir):
    """The package clause of the Go files at the worktree's root."""
    for name in sorted(os.listdir(workdir)):
        if name.endswith(".go"):
            m = re.search(r"^package (\w+)", read_doc(os.path.join(workdir, name)), re.M)
            if m:
                return m.group(1).replace("_test", "")
    return "main"


# -count=1 keeps go's test cache out of it: a [fail-check] test must really
# run every time the checks do.
CHECKS = """\
```gummi-checks
- name: build
  cmd: go build ./...
- name: test
  cmd: go test -count=1 ./...
```"""

# A [fail-check] card's test passes the FIRST time the checks run on its
# branch and fails on every run after that. gummi runs the checks twice once
# the code exists -- for the review pass, then for verify -- and a failure
# at review would stop the card there (the check floors the review), so this
# is what gets a real failing check in front of verify. The marker lives in
# $TMPDIR, keyed by the worktree, so every workspace starts clean.
FAIL_CHECK_TEST = """\
package %(pkg)s

import (
\t"crypto/sha256"
\t"fmt"
\t"os"
\t"path/filepath"
\t"testing"
)

func Test%(name)sRegresses(t *testing.T) {
\twd, _ := os.Getwd()
\tmarker := filepath.Join(os.TempDir(), fmt.Sprintf("gummi-e2e-fail-check-%%x", sha256.Sum256([]byte(wd))))
\tif _, err := os.Stat(marker); err == nil {
\t\tt.Fatal("this card was told to leave a failing check: %(name)s regresses from the second run on")
\t}
\t_ = os.WriteFile(marker, nil, 0o600)
}
"""


# --------------------------------------------------------------------------
# stages
# --------------------------------------------------------------------------

# A [ask-long] card's question: long enough (about 1200 characters, three
# paragraphs, `\n` breaks) that it overflows the decision's cap on every
# viewport, so a test can prove the question and every answer both stay
# reachable rather than one being clipped away.
LONG_QUESTION = (
    "Before the plan is written, %s needs a decision on where its helper lives, "
    "and the three shapes on the table each cost something a one-line summary "
    "would hide.\n"
    "A new file keeps greet.go untouched: the helper and its test live beside "
    "it in their own file, so a reviewer sees one diff for one change and "
    "nothing already there shifts under them. It is the smallest diff of the "
    "three, and it is what the plan recommends.\n"
    "Extending the existing file keeps the file count down, but a second "
    "responsibility lands in greet.go and its test file grows a second "
    "concern alongside the first -- fine today, harder to read back once a "
    "third helper wants to join it.\n"
    "A shared package moves the existing greeting and the new helper into a "
    "package other cards can import, which only pays for itself once another "
    "card is already waiting to reuse it; otherwise it is a package of one "
    "caller, indirection with nothing behind it yet.\n"
    "None of the three changes behavior or the command, and all three pass "
    "the same checks and leave the module in a state a later card can still "
    "build on cleanly -- pick the shape you want this module to keep growing in."
)


def plan_feature(turn, answer=None):
    ctx = turn.ctx
    turn.think("reading the module to see where the change belongs")
    turn.tool("read", "greet.go")
    turn.tool("grep", "func Greet")
    name = ident(ctx)
    problem = ("%s: %s\n\nThe tiny module has one greeting; this card adds a second "
               "exported helper next to it, with a test." % (ctx["card"], ctx["title"]))
    considered = ("1. **A new file** -- `%s.go` beside `greet.go`, one function and "
                  "its test.\n2. **Extend the existing file** -- fewer files, noisier diff." % name.lower())
    write_sections(turn, [
        ("Problem", problem),
        ("Out of scope", "- changing `Greet` or the command"),
        ("Considered approaches", considered),
    ])
    gives_up = "[ask-gives-up]" in ctx["keywords"]
    dies = "[ask-dies]" in ctx["keywords"]
    spends = "[ask-spends]" in ctx["keywords"]
    long_q = "[ask-long]" in ctx["keywords"]
    if ("[ask]" in ctx["keywords"] or "[ask-multi]" in ctx["keywords"] or long_q or gives_up or dies or spends) and answer is None:
        turn.say("Two ways to do this are written up under Considered approaches. "
                 "I need you to pick one.")
        options = [
            {"label": "A new file (recommended)", "detail": "one function and its test, nothing else touched"},
            {"label": "Extend the existing file", "detail": "fewer files, noisier diff"},
        ]
        if long_q:
            options.append({"label": "A shared package", "detail": "a new package other cards can import too"})
        answer = call_tool("ask_user", timeout=ASK_TIMEOUT if gives_up or dies else None,
                           spend=ASK_SPEND if spends else 0, args={
            "question": (LONG_QUESTION % ctx["card"]) if long_q else "Where should %s's helper live?" % ctx["card"],
            "options": options,
            "changes_section": "Chosen approach",
            "spec_anchor": "Chosen approach",
            **({"multi_select": True} if "[ask-multi]" in ctx["keywords"] else {}),
        })
    chosen = "Approach 1: a new file."
    if answer:
        chosen = "Decided with the user: %s" % answer.strip().splitlines()[0]
    write_sections(turn, [
        ("Chosen approach", chosen),
        ("Implementation notes",
         "1. Add `func %s() string` in `%s.go`.\n"
         "2. Add `Test%s` in `%s_test.go`.\n\n"
         "### Plan claims\n\n- `the change is confined to two new files`" % (name, name.lower(), name, name.lower())),
        ("Verification plan", CHECKS + "\n\n- the module builds and its tests pass"),
    ])
    turn.say("Converged on the chosen approach; the implementation notes and the "
             "verification plan are in the spec.")


# The [fold] scenario's pacing is a real sleep, never a FAST beat: the row a
# fold test unfolds between two paced calls exists only while they are.
def plan_fold(turn, answer=None):
    """[fold]: a paced tool pair a fold test can unfold between.

    `read` stays running through one pause, so the test opens its activity
    row while it runs; settling it and appending `grep` each redraw the row.
    After the person answers the ask, writing the chosen approach starts a
    second activity row and the turn holds one more pause before the spec is
    finished, so the test asserts inside the still-live session -- once the
    stage ends, the live block is replaced and its folds are not carried
    across.
    """
    ctx = turn.ctx
    name = ident(ctx)
    call_id = "fold-read-%d" % os.getpid()
    turn.think("reading the module to see where the change belongs")
    emit({"type": "tool", "name": "read", "detail": "greet.go", "id": call_id})
    time.sleep(FOLD_PAUSE)
    turn.tool_result(call_id, "read")
    time.sleep(FOLD_PAUSE / 2)
    turn.tool("grep", "func Greet")
    problem = ("%s: %s\n\nThe tiny module has one greeting; this card adds a second "
               "exported helper next to it, with a test." % (ctx["card"], ctx["title"]))
    considered = ("1. **A new file** -- `%s.go` beside `greet.go`, one function and "
                  "its test.\n2. **Extend the existing file** -- fewer files, noisier diff." % name.lower())
    write_sections(turn, [
        ("Problem", problem),
        ("Out of scope", "- changing `Greet` or the command"),
        ("Considered approaches", considered),
    ])
    turn.say("Two ways to do this are written up under Considered approaches. "
             "I need you to pick one.")
    if answer is None:
        answer = call_tool("ask_user", args={
            "question": "Where should %s's helper live?" % ctx["card"],
            "options": [
                {"label": "A new file (recommended)", "detail": "one function and its test, nothing else touched"},
                {"label": "Extend the existing file", "detail": "fewer files, noisier diff"},
            ],
            "changes_section": "Chosen approach",
            "spec_anchor": "Chosen approach",
        })
    chosen = "Decided with the user: %s" % answer.strip().splitlines()[0]
    write_sections(turn, [("Chosen approach", chosen)])
    time.sleep(FOLD_PAUSE)
    write_sections(turn, [
        ("Implementation notes",
         "1. Add `func %s() string` in `%s.go`.\n"
         "2. Add `Test%s` in `%s_test.go`.\n\n"
         "### Plan claims\n\n- `the change is confined to two new files`" % (name, name.lower(), name, name.lower())),
        ("Verification plan", CHECKS + "\n\n- the module builds and its tests pass"),
    ])
    turn.say("Converged on the chosen approach; the implementation notes and the "
             "verification plan are in the spec.")


def plan_bug(turn):
    write_sections(turn, [
        ("Root cause", "The helper returns the wrong string for an empty name."),
        ("Fix", "1. Guard the empty name.\n2. Add a regression test.\n\n"
                "### Plan claims\n\n- `the change is confined to one file`"),
        ("Verification", CHECKS + "\n\n- the module builds and its tests pass"),
    ])
    turn.say("Found the root cause and wrote the fix plan.")


def plan_research(turn):
    turn.tool("read", "greet.go")
    write_sections(turn, [
        ("Questions", "1. How does the module greet today?"),
        ("Constraints", "- read-only: the survey changes nothing"),
        ("Direction", "Survey `greet.go` and its test; one slice if anything is missing."),
    ])
    turn.say("The question, its constraints and the direction are set.")


def stage_plan(turn, answer=None):
    kind = turn.ctx["kind"]
    if kind == "RS":
        plan_research(turn)
    elif kind == "BG":
        plan_bug(turn)
    elif "[fold]" in turn.ctx["keywords"]:
        plan_fold(turn, answer)
    else:
        plan_feature(turn, answer)


def stage_implement(turn):
    ctx = turn.ctx
    wd = ctx["workdir"]
    name = ident(ctx)
    turn.think("applying the implementation notes")
    turn.tool("read", "greet.go")
    pkg = go_package(wd)
    src, test_src = name.lower() + ".go", name.lower() + "_test.go"
    write_file(wd, src,
               "package %s\n\n// %s is %s's helper.\nfunc %s() string { return \"%s\" }\n"
               % (pkg, name, ctx["card"], name, ctx["card"]))
    turn.tool("edit", src)
    test = ("package " + pkg + "\n\nimport \"testing\"\n\nfunc Test%s(t *testing.T) {\n"
            "\tif got := %s(); got != \"%s\" {\n\t\tt.Fatalf(\"got %%q\", got)\n\t}\n}\n"
            % (name, name, ctx["card"]))
    if "[fail-check]" in ctx["keywords"]:
        test = FAIL_CHECK_TEST % {"pkg": pkg, "name": name}
    write_file(wd, test_src, test)
    turn.tool("edit", test_src)
    turn.tool("bash", "go build ./...")
    git(wd, "add", "--", src, test_src)
    git(wd, "commit", "-q", "-m", "feat: add %s\n\nThe helper %s asked for, with its test." % (name, ctx["card"]))
    turn.tool("bash", "git commit")
    turn.say("Implemented `%s` and its test, and committed." % name)


def stage_rebase(turn, kickoff):
    """Run the rebase the kickoff names and resolve what stops it, the way
    the contract asks: a conflicted path takes this card's side, and an
    untracked file in the way is moved aside and folded back in after."""
    wd = turn.ctx["workdir"]
    m = re.search(r"run `(git rebase [^`]+)`", kickoff)
    if not m:
        turn.say("The kickoff names no rebase command; nothing to do.")
        return

    def out(*args):
        return subprocess.run(["git", "-C", wd] + list(args), check=False,
                              capture_output=True, text=True)

    def in_progress():
        for d in ("rebase-merge", "rebase-apply"):
            p = out("rev-parse", "--git-path", d).stdout.strip()
            if p and os.path.exists(p if os.path.isabs(p) else os.path.join(wd, p)):
                return True
        return False

    aside = {}
    res = out(*m.group(1).split()[1:])
    for _ in range(12):
        blocked = re.findall(r"^\t(\S+)$", res.stderr, re.M) if "would be overwritten" in res.stderr else []
        for f in blocked:
            src = os.path.join(wd, f)
            if os.path.exists(src):
                aside[f] = open(src, encoding="utf-8").read()
                os.remove(src)
        if blocked and not in_progress():
            res = out(*m.group(1).split()[1:])
            continue
        if not in_progress():
            break
        for f in out("diff", "--name-only", "--diff-filter=U").stdout.split():
            out("checkout", "--theirs", "--", f)
            out("add", "--", f)
        res = out("-c", "core.editor=true", "rebase", "--continue")
    for f, text in aside.items():
        path = os.path.join(wd, f)
        base = open(path, encoding="utf-8").read() if os.path.exists(path) else ""
        write_file(wd, f, base + text if text not in base else base)
    turn.say("Rebased with `%s`; conflicts took this card's side%s." % (
        m.group(1), "" if not aside else ", and %s was folded back in" % ", ".join(sorted(aside))))


def verdict(turn, value, summary, text):
    turn.say(text + "\n\nVERDICT: " + value)
    if turn.ctx["has_verdict_tool"]:
        call_tool("submit_verdict", {"verdict": value, "summary": summary})


def stage_critique(turn):
    turn.tool("read", os.path.basename(turn.ctx["spec"]) or "spec")
    verdict(turn, "pass", "nothing blocking",
            "Walked the plan through every lens. Nothing blocking.")


def stage_review(turn):
    turn.tool("bash", "git diff")
    verdict(turn, "pass", "nothing blocking",
            "**conformance** -- nothing blocking.\n**standards** -- nothing further.")


def stage_verify(turn, kickoff):
    turn.tool("read", os.path.basename(turn.ctx["spec"]) or "spec")
    live = [ln.strip() for ln in kickoff.split("\n")
            if re.search(r": (FAIL \(exit|TIMEOUT|NOT RUN)", ln)]
    if live:
        verdict(turn, "fail", "a check regressed",
                "The branch regressed a check:\n\n" + "\n".join(live))
    elif "[fail-verify]" in turn.ctx["keywords"]:
        verdict(turn, "fail", "the verification plan does not hold",
                "The checks pass, but the verification plan's bullet does not hold on this branch.")
    else:
        verdict(turn, "pass", "everything verified",
                "Every check passed and the verification plan holds.")


def freeform_turn(turn, text):
    if STEP_RE.search(text):
        workflow_step(turn, text)
        return
    if STEP:
        step_followup(turn, text)
        return
    if re.match(r"Workflow run \d+ has reported", text):
        # the run's report reaches the session as a turn; reading it
        # changes nothing in the session's tree
        turn.say("The run reported; its changes are in the tree for you to read.")
        return
    if "[workflow" in text:
        workflow_session(turn, text)
        return
    if "[watch]" in text:
        # a Monitor watch the turn leaves open: no result comes for it, so
        # the card reads as watching once the turn is over
        turn.tool("Monitor", "tail -f build.log", "ff-watch")
        turn.say("Watching the build; I will tell you when it breaks.")
        return
    answered = re.search(r"^The answer is: (.*)$", text, re.M)
    if answered and turn.ctx.get("step_brief"):
        # a step's answer reaching a fresh process: its brief rides the
        # hints, so the step does its task with the answer in hand
        workflow_step(turn, turn.ctx["step_brief"], answered.group(1).strip())
        return
    if answered:
        # an answer that reaches the session as a turn of its own (its
        # backend was replaced, or gave up on the call that asked)
        turn.say("Decided with the user: %s" % answered.group(1).strip())
        return
    if any(k in text for k in SESSION_ASKS):
        session_where_ask(turn, text)
        return
    if "[ask]" in text:
        # blocks inside ask_user until the person answers, then echoes the
        # answer as the tool result's words, so a test can read them back
        answer = call_tool("ask_user", args={
            "question": "Which way?",
            "options": [{"label": "Left"}, {"label": "Right"}],
            "changes_section": "Chosen approach",
        })
        turn.say("You said: %s" % answer.strip().splitlines()[0])
        return
    if "[spec]" in text:
        # the session writes its spec first, then edits its tree as any
        # turn does, so a test has a spec and a diff to read
        call_tool("spec_start", args={"title": "Notes for the helper"})
        call_tool("spec_replace_section", args={"section": "Problem", "body": "The helper needs a note.\n"})
        call_tool("spec_replace_section", args={"section": "Approach", "body": "Write it in NOTES.md.\n"})
        call_tool("spec_replace_section", args={"section": "Done when", "body": WORKFLOW_DONE_WHEN})
    wd = turn.ctx["workdir"]
    notes = os.path.join(wd, "NOTES.md")
    with open(notes, "a", encoding="utf-8") as fh:
        fh.write("- %s\n" % text.strip().splitlines()[0] if text.strip() else "- (empty)\n")
    turn.think("The ask belongs in NOTES.md, beside the others.")
    turn.tool("edit", "NOTES.md", "ff-edit")
    git(wd, "add", "--", "NOTES.md")
    turn.tool_result("ff-edit", "edit")
    second = "completed" if "[tasks-done]" in text else "in_progress"
    turn.tasks(("Note the fix", "completed"), ("Say what was done", second))
    turn.say("Done: noted it in NOTES.md.")


# The question variants a session can be started with: each asks where the
# helper lives, the way the retired plan stage's [ask] did, and its answer
# comes back as "Decided with the user: <answer>".
SESSION_ASKS = ("[ask-where]", "[ask-multi]", "[ask-long]", "[ask-gives-up]", "[ask-dies]", "[ask-spends]")


def session_where_ask(turn, text):
    ctx = turn.ctx
    gives_up = "[ask-gives-up]" in text
    dies = "[ask-dies]" in text
    spends = "[ask-spends]" in text
    long_q = "[ask-long]" in text
    turn.say("There are two ways to place the helper. I need you to pick one.")
    options = [
        {"label": "A new file (recommended)", "detail": "one function and its test, nothing else touched"},
        {"label": "Extend the existing file", "detail": "fewer files, noisier diff"},
    ]
    if long_q:
        options.append({"label": "A shared package", "detail": "a new package other cards can import too"})
    answer = call_tool("ask_user", timeout=ASK_TIMEOUT if gives_up or dies else None,
                       spend=ASK_SPEND if spends else 0, args={
        "question": (LONG_QUESTION % ctx["card"]) if long_q else "Where should %s's helper live?" % ctx["card"],
        "options": options,
        **({"multi_select": True} if "[ask-multi]" in text else {}),
    })
    turn.say("Decided with the user: %s" % answer.strip().splitlines()[0])


WORKFLOW_DONE_WHEN = """```gummi-done-when
- id: DW-1
  says: made.txt exists
  check: test -f made.txt
- id: DW-2
  says: made.txt reads well
  judge: true
```
"""

# the step brief's head: "You are step build/1/implement of workflow run 1
# (the deliver recipe) in gummi: a implementer. ..."
STEP_RE = re.compile(r"You are step (\S+) of workflow run (\d+) \(([^)]*)\) in gummi: an? (\w+)\.")

# STEP is the step this process answers for, once its brief has arrived: a
# step's card keeps its agent across its turns, so a later turn on it (a
# carry-on, a steer, the person's message) is the step's too.
STEP = {}


def workflow_session(turn, text):
    """A session turn asking for a workflow: a spec with done-when items, then workflow_run."""
    m = re.search(r"\[workflow=([\w-]+)\]", text)
    recipe = m.group(1) if m else "deliver"
    # a second [workflow] turn finds a spec already there and adds another;
    # the run then names the newest
    started = call_tool("spec_start", args={"title": "Make the workflow file"})
    n = re.search(r"\bspec (\d+)\b", started)
    spec = int(n.group(1)) if n else 1
    asks = " [step-asks]" if "[step-asks]" in text else ""
    for section, body in (("Problem", "made.txt is missing.%s\n" % asks),
                          ("Approach", "The workflow's steps write it.\n"),
                          ("Done when", WORKFLOW_DONE_WHEN)):
        call_tool("spec_replace_section", args={"spec": spec, "section": section, "body": body})
    result = call_tool("workflow_run", args={"recipe": recipe, "spec": spec})
    turn.say("workflow_run: %s" % result.strip())


def workflow_step(turn, brief, answer=""):
    """A workflow step's turn, answered by its role (DESIGN §25.4). answer
    is the person's answer to the step's question when it reaches a fresh
    process (the one that asked has ended): the step carries on with it."""
    m = STEP_RE.search(brief)
    path, role = m.group(1), m.group(4)
    STEP.update(path=path, role=role, brief=brief)
    sid = path.rsplit("/", 1)[-1]
    task = brief.split("Your task:", 1)[-1]
    wd = turn.ctx["workdir"]
    artifacts = re.findall(r"(/\S+?/\.gummi/runs/\S+?\.(?:md|json))", task)
    if role == "reviewer":
        review = [p for p in artifacts if p.endswith("review.json")]
        if review:
            # the verdict step: the reviews merged into the artifact a
            # repeat's until reads
            os.makedirs(os.path.dirname(review[0]), exist_ok=True)
            with open(review[0], "w", encoding="utf-8") as fh:
                json.dump({"verdict": "APPROVED", "findings": []}, fh)
            turn.tool("write", os.path.basename(review[0]), "step-write")
            turn.tool_result("step-write", "write")
            turn.say("The reviews agree.\nAPPROVED")
        elif "MET: DW-" in task:
            # the validate step: one line per done-when item of the spec
            ids = sorted(set(re.findall(r"^- id: (DW-\d+)", task, re.M))) or ["DW-1"]
            turn.tool("read", "the run's diff")
            turn.say("Read the run's diff against the spec.\n" + "\n".join("MET: %s" % i for i in ids))
        else:
            turn.tool("read", "the run's diff")
            turn.say("Read %s's change against the spec. No findings.\nAPPROVED" % sid)
        return
    if role == "scout":
        turn.tool("read", "the tree")
        turn.say("Mapped what bears on the spec.\nDECISION: none")
        return
    if role == "planner":
        turn.say("The plan stands.")
        return
    # a writing role: its artifacts, and what the spec's checks look for
    wrote = []
    for p in artifacts:
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as fh:
            fh.write("# %s\n\nWritten by step %s.\n" % (sid, path))
        wrote.append(os.path.basename(p))
    if answer:
        STEP["asked"] = True
    if role == "implementer" and "[step-asks]" in task and not STEP.get("asked"):
        # blocks until the person answers the step's question
        STEP["asked"] = True
        answer = call_tool("ask_user", args={
            "question": "Keep made.txt plain, or give it a header?",
            "options": [{"label": "Keep it plain (recommended)", "detail": "one line per step"},
                        {"label": "Give it a header"}],
        }).strip()
    if role in ("implementer", "tester"):
        files = re.findall(r"check:\s*\"?test -f (\S+?)\"?\s*$", task, re.M) or ["WORKFLOW.md"]
        for f in files:
            target = os.path.join(wd, f)
            os.makedirs(os.path.dirname(target) or wd, exist_ok=True)
            with open(target, "a", encoding="utf-8") as fh:
                fh.write("made by step %s\n" % path)
            wrote.append(f)
    turn.tool("edit", ", ".join(wrote) or "nothing", "step-edit")
    turn.tool_result("step-edit", "edit")
    told = " The person said: %s." % answer if answer else ""
    turn.say("Wrote %s.%s\nDECISION: plain text, one line" % (", ".join(wrote) or "nothing", told))


def step_followup(turn, text):
    """A later turn on a step's card: a carry-on runs its task again; any
    other line (a steer, the person's message) is acknowledged."""
    if text.startswith("The run was paused and now carries on"):
        workflow_step(turn, STEP["brief"])
        return
    turn.say("Noted: %s" % (text.strip().splitlines() or [""])[0])


def ingest(turn):
    """Decompose the ingest source: one feature per `## ` heading."""
    ctx = turn.ctx
    src = os.path.join(ctx["workdir"], ctx["ingest_source"])
    turn.tool("read", ctx["ingest_source"])
    doc = read_doc(src)
    titles = [ln[3:].strip() for ln in doc.split("\n") if ln.startswith("## ") and ln[3:].strip()]
    # a heading starting "Unmapped:" is a requirement no proposal covers
    unmapped = [t for t in titles if t.lower().startswith("unmapped:")]
    titles = [t for t in titles if t not in unmapped]
    if not titles:
        m = re.search(r"^# (.+)$", doc, re.M)
        titles = [m.group(1).strip() if m else "Ingested work"]
    features = [{
        "title": t,
        "one_liner": "the %s part of the document" % t.lower(),
        "source_refs": [t],
        "depends_on": [],
        "problem": "The document asks for %s." % t.lower(),
        "constraints": "",
        "acceptance": "- %s is done and tested" % t,
        "open_questions": [],
    } for t in titles]
    coverage = [{"requirement": t, "feature": t, "status": "mapped", "note": ""} for t in titles]
    coverage += [{"requirement": t[len("unmapped:"):].strip(), "feature": "", "status": "unmapped",
                  "note": "no proposal covers it"} for t in unmapped]
    proposal = {"features": features, "coverage": coverage}
    if ctx["has_propose_tool"]:
        call_tool("propose_features", proposal)
        turn.say("Proposed %d features." % len(features))
    else:
        turn.say("```gummi-propose\n%s\n```" % json.dumps(proposal, indent=1))


def chat_reply(turn, text):
    first = text.strip().splitlines()[0] if text.strip() else ""
    turn.say("Noted: %s" % first[:200])


# --------------------------------------------------------------------------
# scribe one-shots -- recognised by the prompt, not by a stage
# --------------------------------------------------------------------------

def scribe(ctx, prompt):
    """Answer a scribe prompt. Returns the reply text, or None if not one."""
    if "ESTIMATE:" in prompt:
        return "ESTIMATE: 120"
    if "squash-merge landing commit" in prompt:
        # the engine accepts a reply that is entirely one gummi-commit fence
        return ("```gummi-commit\nfeat: land %s\n\n- the helper and its test\n```"
                % (ctx["card"] or "the card"))
    if "CLAIM: <your one sentence>" in prompt:
        return "CLAIM: the branch does what the plan says\nANCHOR: spec:Verification plan"
    if "INTENT: <one of the words above>" in prompt:
        m = re.search(r"The sentence:\s*```\n(.*?)\n```", prompt, re.S)
        line = (m.group(1) if m else "").lower().strip()
        intent = "implementation_wrong"
        if line.endswith("?"):
            intent = "question"
        elif line in ("go", "ok", "yes") or "approve" in line or "looks right" in line:
            intent = "proceed"
        elif "separate" in line or "another card" in line:
            intent = "separate_card"
        elif "test" in line or "check" in line:
            intent = "check_missing"
        elif "approach" in line or "design" in line:
            intent = "plan_wrong"
        return "INTENT: " + intent
    if "determine the fixed commands" in prompt or "build/test/lint" in prompt or "checks block" in prompt:
        return CHECKS
    return None


# --------------------------------------------------------------------------
# session
# --------------------------------------------------------------------------

KEYWORDS = ("[ask-where]", "[ask-multi]", "[ask-long]", "[fail-check]", "[fail-verify]", "[ask]", "[ask-gives-up]", "[ask-dies]", "[ask-spends]", "[fold]", "[slow]", "[research]")


def detect(frame):
    hints = "\n".join(frame.get("hints") or [])
    ctx = {
        "workdir": frame.get("workdir") or os.getcwd(),
        "model": frame.get("model") or "e2e",
        "has_verdict_tool": any(t.get("name") == "submit_verdict" for t in frame.get("tools") or []),
        "has_propose_tool": any(t.get("name") == "propose_features" for t in frame.get("tools") or []),
        "card": "", "kind": "", "title": "", "stage": "", "spec": "",
    }
    m = re.search(r"You are the \w+ for \w+ ((FD|BG|RS|FF)-\d+): (.*)", hints)
    if m:
        ctx["card"], ctx["kind"], ctx["title"] = m.group(1), m.group(2), m.group(3).strip().rstrip(".")
    else:
        m = re.search(r"\b((FD|BG|RS|FF)-\d+)\b", hints)
        if m:
            ctx["card"], ctx["kind"] = m.group(1), m.group(2)
    if "Task: Rebase onto" in hints:
        # the rebase-resolve pass borrows the card's stage; it is its own job
        ctx["stage"] = "rebase"
    elif "a freeform card" in hints:
        ctx["stage"] = "open"
    elif re.search(r"^(Stage: Plan critique|Research critique)", hints, re.M):
        ctx["stage"] = "critique"
    else:
        m = re.search(r"^Stage: (\w+)", hints, re.M)
        if m:
            ctx["stage"] = m.group(1).lower()
    m = re.search(r"The source document is at (\S+?)\.? \(relative to your working directory\)", hints)
    if m:
        ctx["ingest_source"] = m.group(1)
    if STEP_RE.search(hints):
        # a step's card restarted: its brief is in the hints
        ctx["step_brief"] = hints[STEP_RE.search(hints).start():]
    m = re.search(r"at (/\S+\.md)", hints)
    if m:
        ctx["spec"] = m.group(1).rstrip(".")
    # the scenario keywords, from the title/brief in the hints and the artifact
    blob = (hints + "\n" + read_doc(ctx["spec"])).lower()
    ctx["keywords"] = [k for k in KEYWORDS if k in blob]
    return ctx


def handle_send(ctx, text, first):
    turn = Turn(ctx)
    if STEP_RE.search(text) or STEP:
        # a workflow step's turn: its brief quotes the spec, which no
        # scribe pattern may read as one of its prompts
        freeform_turn(turn, text)
        turn.usage()
        return
    if text.lstrip().startswith("[slow]"):
        # a line that opens with [slow] asks to be streamed slowly; one that
        # merely quotes a [slow] card's title does not
        turn.slow = True
    reply = scribe(ctx, text)
    if reply is not None:
        emit({"type": "text", "text": reply})
        turn.usage(2)
        return
    stage = ctx["stage"]
    answered = re.search(r"^The answer is: (.*)$", text, re.M)
    if ctx.get("ingest_source") and first:
        ingest(turn)
    elif stage == "open":
        freeform_turn(turn, text)
    elif not first and not answered:
        chat_reply(turn, text)
    elif stage == "plan":
        stage_plan(turn, answered.group(1) if answered else None)
    elif stage == "critique":
        stage_critique(turn)
    elif stage == "rebase":
        stage_rebase(turn, text)
    elif stage == "implement":
        stage_implement(turn)
    elif stage == "review":
        stage_review(turn)
    elif stage == "verify":
        stage_verify(turn, text)
    else:
        chat_reply(turn, text)
    turn.usage()


def main():
    threading.Thread(target=reader, daemon=True).start()
    ctx = {"stage": "", "card": "", "kind": "", "title": "", "spec": "", "keywords": [],
           "workdir": os.getcwd(), "model": "e2e", "has_verdict_tool": False}
    first = True
    while True:
        frame = next_frame()
        kind = frame.get("type")
        if kind == "init":
            ctx = detect(frame)
            print("web-e2e-agent: card=%s stage=%s keywords=%s" % (ctx["card"], ctx["stage"], ctx["keywords"]),
                  file=sys.stderr)
        elif kind == "send":
            try:
                handle_send(ctx, frame.get("text") or "", first)
            except Interrupted:
                emit({"type": "message", "text": "(stopped)"})
            except TimedOut:
                if "[ask-dies]" in ctx["keywords"]:
                    print("web-e2e-agent: dying behind an open question", file=sys.stderr)
                    sys.stderr.flush()
                    os._exit(3)
                emit({"type": "message", "text": "The ask timed out. That question is live in "
                                                 "your pane now; take your time answering it."})
            first = False
            emit({"type": "idle"})
        elif kind == "interrupt":
            emit({"type": "idle"})
        # a stray resolve (for a call we stopped waiting on) is ignored


if __name__ == "__main__":
    try:
        main()
    except (BrokenPipeError, KeyboardInterrupt, Closed):
        pass
