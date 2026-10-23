#!/usr/bin/env python3
"""A scripted agent for the gummi demo recording.

Speaks gummi's headless backend protocol (internal/agent/headless.go) on
stdio, so the engine, the worktrees, the session's spec, the workflow
run, its checks and the diff are all real -- only the model's words
are pre-authored. That makes the demo deterministic and offline: every
take is identical, and a re-record after a UI change costs one command.

Wire in with:

    GUMMI_AGENT=headless GUMMI_AGENT_CMD=scripts/demo-agent.py gummi

The hero is a session titled "lxc list: add a disk usage percent column"
in a clone of canonical/lxd. Its story, turn by turn:

  session     reads lxc/list.go, asks which denominator (ask_user), writes
              a spec with four done-when items, then offers the deliver
              workflow (ask_user); on yes it calls workflow_run
  design      writes design.md in the run's directory
  implement   adds the column, its shorthand entry and its test -- but not
              the help line, so both reviewers find it and the verdict
              writes CHANGES; the build loops once, and the second
              implement adds the help line
  verdict     APPROVED once the help line is there
  validate    MET for every done-when item; the report reaches the session
  session     explains the report; told to commit it, commits the tree

Every other session (the board's seeded ones) gets a short, generic turn
that edits one file and commits it. A session whose title ends in "?"
asks its person a question first and stops there.

Environment:
    GUMMI_DEMO_FAST=1     skip the pacing beats (the seed runs offscreen)
    GUMMI_DEMO_DEBUG=path append every frame in and out to this file
"""

import json
import os
import queue
import re
import subprocess
import sys
import threading
import time

FAST = os.environ.get("GUMMI_DEMO_FAST") == "1"
DEBUG = os.environ.get("GUMMI_DEMO_DEBUG")

# The hero session is created live during the recording, so its number
# depends on how many cards the seed made first. Recognise it by title.
HERO_MARKER = "disk usage percent"


# --------------------------------------------------------------------------
# wire
# --------------------------------------------------------------------------

def log(direction, obj):
    if not DEBUG:
        return
    with open(DEBUG, "a", encoding="utf-8") as fh:
        fh.write("%s %d %s\n" % (direction, os.getpid(), json.dumps(obj)[:600]))


def emit(obj):
    log("OUT", obj)
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


# Frames arrive on a reader thread, so a tool call can block on its
# resolve frame while anything else that arrives waits its turn.
INBOX = queue.Queue()
DEFERRED = []


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
    INBOX.put(None)


class Closed(Exception):
    pass


def next_frame():
    if DEFERRED:
        return DEFERRED.pop(0)
    frame = INBOX.get()
    if frame is None:
        raise Closed()
    return frame


def call_tool(name, args):
    """Invoke one of gummi's tools and block until gummi resolves it.

    ask_user resolves when the person answers; the rest resolve at once.
    """
    call_id = "%s-%d-%d" % (name, os.getpid(), int(time.time() * 1000))
    emit({"type": "ask", "id": call_id, "name": name, "ask": args})
    while True:
        frame = INBOX.get()
        if frame is None:
            raise Closed()
        if frame.get("type") == "resolve" and frame.get("id") == call_id:
            return frame.get("result") or ""
        DEFERRED.append(frame)


def beat(seconds):
    if not FAST:
        time.sleep(seconds)


def say(text, pace=0.6):
    beat(pace)
    emit({"type": "message", "text": text})


def think(text, pace=0.4):
    beat(pace)
    emit({"type": "reasoning", "text": text})


def tool(name, detail, pace=0.4):
    beat(pace)
    emit({"type": "tool", "name": name, "detail": detail})


def usage(ctx, credits, inp=0, out=0):
    emit({"type": "usage", "credits": credits, "input": inp, "output": out, "model": ctx["model"]})


def git(workdir, *args):
    subprocess.run(["git", "-C", workdir] + list(args), check=False,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def first_line(text):
    text = (text or "").strip()
    return text.splitlines()[0] if text else ""


# --------------------------------------------------------------------------
# the hero session's spec
# --------------------------------------------------------------------------

PROBLEM = """\
`lxc list` shows memory both absolutely (`-c m`, MEMORY USAGE) and as a
percentage of the instance's limit (`-c M`, MEMORY USAGE%). Disk has only
the absolute form: `-c D` renders DISK USAGE and there is no percentage
counterpart, so "how full is this instance's root disk?" -- the question
behind most capacity alerts -- takes a second command per instance.

The numerator is already fetched: `diskUsageColumnData` reads
`State.Disk[rootDisk].Usage` (lxc/list.go). Only the denominator and a
column entry are missing."""

APPROACH = """\
Report against `State.Disk[rootDisk].Total`, the pool-reported total that
arrives on the request already carrying `Usage` -- not the root device's
`size` quota, which is unset on most instances and would blank the column
exactly where operators need it.

`Total` is 0 both when the driver cannot report one and when the instance
sees the whole pool; neither is an honest percentage, so the cell renders
blank, as `-c M` does for an unset `limits.memory`.

Shorthand `U`: `d` is Description and `D` is DISK USAGE, so the disk pair
cannot mirror `m`/`M`. Out of scope: sorting by the column, other
resources, and any server-side change."""

DONE_WHEN = """\
```gummi-done-when
- id: DW-1
  says: the lxc client builds
  check: go build ./lxc/...
- id: DW-2
  says: go vet is clean on the lxc package
  check: go vet ./lxc/
- id: DW-3
  says: the new column's function is tested, Total == 0 included
  check: go test ./lxc/ -run TestDiskUsagePercentColumn
- id: DW-4
  says: lxc list --help lists U beside D, and bare lxc list output is unchanged
  judge: true
```
"""

# --------------------------------------------------------------------------
# the change the workflow makes to the real lxd tree
# --------------------------------------------------------------------------

IMPL_GO = '''
func (c *cmdList) diskUsagePercentColumnData(cInfo api.InstanceFull) string {
	rootDisk, _, _ := api.GetRootDiskDevice(cInfo.ExpandedDevices)

	if cInfo.State == nil || cInfo.State.Disk == nil {
		return ""
	}

	disk := cInfo.State.Disk[rootDisk]

	// Total is 0 both when the storage driver cannot report a total and
	// when the instance has access to the whole pool; neither is a
	// percentage we can honestly print, so render an empty cell.
	if disk.Usage <= 0 || disk.Total <= 0 {
		return ""
	}

	return fmt.Sprintf("%.1f%%", (float64(disk.Usage)/float64(disk.Total))*float64(100))
}
'''

TEST_GO = '''
func TestDiskUsagePercentColumn(t *testing.T) {
	root := map[string]map[string]string{
		"root": {"type": "disk", "path": "/", "pool": "default"},
	}

	state := func(usage int64, total int64) *api.InstanceState {
		return &api.InstanceState{Disk: map[string]api.InstanceStateDisk{
			"root": {Usage: usage, Total: total},
		}}
	}

	tests := []struct {
		name  string
		state *api.InstanceState
		want  string
	}{
		{"half full", state(512*1024*1024, 1024*1024*1024), "50.0%"},
		{"whole pool or no total", state(512*1024*1024, 0), ""},
		{"no usage reported", state(0, 1024*1024*1024), ""},
		{"no state", nil, ""},
	}

	c := &cmdList{}
	for _, tt := range tests {
		got := c.diskUsagePercentColumnData(api.InstanceFull{
			Instance: api.Instance{ExpandedDevices: root},
			State:    tt.state,
		})
		if got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
'''

HELP_LINE = "  U - Disk usage (%)"


def patch_list_go(workdir, with_help):
    """Add the column to lxc/list.go: the function and the shorthand entry,
    and the help line when with_help is set."""
    path = os.path.join(workdir, "lxc", "list.go")
    with open(path, encoding="utf-8") as fh:
        src = fh.read()
    if "diskUsagePercentColumnData" not in src:
        anchor = "func (c *cmdList) typeColumnData(cInfo api.InstanceFull) string {"
        src = src.replace(anchor, IMPL_GO.strip() + "\n\n" + anchor, 1)
        dline = "\t\t'D': {\"DISK USAGE\", c.diskUsageColumnData, true, false, true, false},"
        src = src.replace(dline, dline + "\n\t\t'U': {\"DISK USAGE%\", c.diskUsagePercentColumnData, true, false, true, false},", 1)
    if with_help and HELP_LINE not in src:
        src = src.replace("  D - disk usage", "  D - disk usage\n" + HELP_LINE, 1)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(src)


def patch_list_test_go(workdir):
    path = os.path.join(workdir, "lxc", "list_test.go")
    with open(path, encoding="utf-8") as fh:
        src = fh.read()
    if "TestDiskUsagePercentColumn" not in src:
        src = src.rstrip("\n") + "\n" + TEST_GO
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(src)


# --------------------------------------------------------------------------
# the hero session
# --------------------------------------------------------------------------

def hero_kickoff(ctx):
    think("reading lxc/list.go to see how the memory pair is built")
    tool("read", "lxc/list.go")
    tool("grep", "columnsShorthandMap")
    tool("read", "shared/api/instance_state.go")
    say("`columnsShorthandMap` (lxc/list.go) carries `m` and `M` for memory but\n"
        "only `D` for disk, and `diskUsageColumnData` already reads\n"
        "`State.Disk[rootDisk].Usage`. So this is a denominator and a column\n"
        "entry. One choice is yours before I write it down.")
    usage(ctx, 38, 9100, 640)
    call_tool("ask_user", {
        "question": "Which denominator should DISK USAGE% report against?",
        "options": [
            {"label": "The pool-reported total (recommended)",
             "detail": "real capacity, on the request that already carries Usage"},
            {"label": "The root device's size quota",
             "detail": "mirrors limits.memory, but is unset on most instances"},
        ],
    })
    think("writing the agreement down as the session's spec")
    call_tool("spec_start", {"title": "lxc list: a DISK USAGE% column"})
    call_tool("spec_replace_section", {"section": "Problem", "body": PROBLEM})
    call_tool("spec_replace_section", {"section": "Approach", "body": APPROACH})
    call_tool("spec_replace_section", {"section": "Done when", "body": DONE_WHEN})
    say("The pool total it is. The spec now holds the problem, the approach\n"
        "and four done-when items -- three of them commands gummi can run.\n\n"
        "It is worth a design, a reviewed build and a validation.")
    usage(ctx, 44, 11800, 920)
    answer = call_tool("ask_user", {
        "question": "Run the deliver workflow on it? A designer, then a build two fresh reviewers check, then a validation.",
        "options": [
            {"label": "Run deliver (recommended)",
             "detail": "you allow the run before anything starts"},
            {"label": "Keep it here", "detail": "I make the change in this session"},
        ],
    })
    if "keep" in answer.lower():
        say("Then I'll do it here.")
        return
    result = call_tool("workflow_run", {"recipe": "deliver"})
    say("Proposed a run: %s" % first_line(result))
    usage(ctx, 12, 3100, 160)


def hero_report(ctx):
    think("reading the run's report against the spec")
    say("The run is done and validated. All three checks ran in this tree --\n"
        "build, vet and the new test -- and the validate step judged DW-4\n"
        "against the run's diff.\n\n"
        "Worth knowing: the build looped once, because the first implement\n"
        "left `U` out of `lxc list --help` and both reviewers caught it. The\n"
        "changes are in the tree, uncommitted. Say \"commit it\" and I will.")
    usage(ctx, 22, 8400, 300)


def hero_commit(ctx):
    wd = ctx["workdir"]
    git(wd, "add", "-A", "lxc")
    git(wd, "commit", "-qm", "lxc/list: add a DISK USAGE% column under -c U")
    tool("bash", "git commit")
    say("Committed the run's change on this session's branch, with every\n"
        "done-when item met.")
    usage(ctx, 6, 2000, 80)


def hero_turn(ctx, text):
    low = text.lower()
    if re.match(r"Workflow run \d+ has reported", text):
        hero_report(ctx)
    elif "commit" in low and len(low) < 80:
        hero_commit(ctx)
    elif ctx["first"]:
        hero_kickoff(ctx)
    else:
        say("Noted: %s" % first_line(text)[:200])
        usage(ctx, 4)


# --------------------------------------------------------------------------
# the workflow's steps -- each a fresh agent, told apart by its brief
# --------------------------------------------------------------------------

STEP_RE = re.compile(r"You are step (\S+) of workflow run (\d+) \(([^)]*)\) in gummi: an? (\w+)\.")


def help_listed(workdir):
    with open(os.path.join(workdir, "lxc", "list.go"), encoding="utf-8") as fh:
        return HELP_LINE in fh.read()


def step_turn(ctx, text):
    wd = ctx["workdir"]
    m = STEP_RE.search(text)
    if m:
        ctx["step"] = m.group(1)
    path = ctx.get("step", "")
    sid = path.rsplit("/", 1)[-1]
    task = text.split("Your task:", 1)[-1]
    artifacts = re.findall(r"(/\S+?/\.gummi/runs/\S+?\.(?:md|json))", task)
    if sid == "design":
        think("the memory pair is the pattern: mirror it")
        tool("read", "lxc/list.go")
        for p in artifacts:
            os.makedirs(os.path.dirname(p), exist_ok=True)
            with open(p, "w", encoding="utf-8") as fh:
                fh.write("# Design\n\n- diskUsagePercentColumnData beside diskUsageColumnData\n"
                         "- registered as 'U' in columnsShorthandMap\n- `U - Disk usage (%)` under `D` in the help\n"
                         "- a table test, Total == 0 included\n")
        tool("write", "design.md")
        say("Wrote the design: the column, its entry, the help line, the test.\n"
            "DECISION: shorthand U, since d and D are taken")
        usage(ctx, 24, 8000, 500)
    elif sid == "implement":
        if re.search(r"/\d+/", path) and not path.startswith("build/1/"):
            think("the reviewers are right: the help text never learned about U")
            patch_list_go(wd, with_help=True)
            tool("edit", "lxc/list.go")
            say("Added `U - Disk usage (%)` under `D - disk usage`.\n"
                "DECISION: kept it beside D so the disk pair reads together")
            usage(ctx, 18, 6000, 240)
        else:
            think("the column function below diskUsageColumnData, then its entry and its test")
            patch_list_go(wd, with_help=False)
            tool("edit", "lxc/list.go")
            patch_list_test_go(wd)
            tool("edit", "lxc/list_test.go")
            tool("bash", "go test ./lxc/ -run TestDiskUsagePercentColumn")
            say("Added `diskUsagePercentColumnData`, registered it as `'U'`, and\n"
                "its table test.\nDECISION: a blank cell when Total is 0, as the spec says")
            usage(ctx, 60, 18000, 1600)
    elif sid in ("review-a", "review-b"):
        think("fresh context: the spec and the run's diff")
        tool("read", "the run's diff")
        if help_listed(wd):
            say("The column, its guard and its test match the spec. No findings.")
        else:
            say("The column is right, and the blank-cell guard is commented.\n"
                "Finding: DW-4 needs `U` in `lxc list --help`, and the diff never adds it.")
        usage(ctx, 20, 12000, 400)
    elif sid == "verdict":
        ok = help_listed(wd)
        for p in artifacts:
            if p.endswith("review.json"):
                os.makedirs(os.path.dirname(p), exist_ok=True)
                with open(p, "w", encoding="utf-8") as fh:
                    json.dump({"verdict": "APPROVED" if ok else "CHANGES",
                               "findings": [] if ok else ["lxc list --help does not list U - Disk usage (%)"]}, fh)
        tool("write", "review.json")
        say("APPROVED" if ok else "CHANGES: list U in lxc list --help")
        usage(ctx, 8, 3000, 120)
    elif sid == "validate":
        think("each done-when item against the run's diff")
        tool("read", "the run's diff")
        say("`lxc list --help` lists `U` beside `D`, and `defaultColumns` is\n"
            "untouched, so bare `lxc list` output is unchanged.\n"
            "MET: DW-1\nMET: DW-2\nMET: DW-3\nMET: DW-4")
        usage(ctx, 20, 12000, 400)
    else:
        say("Nothing to do here.")
        usage(ctx, 4)


# --------------------------------------------------------------------------
# every other session
# --------------------------------------------------------------------------

def generic_turn(ctx, text):
    wd = ctx["workdir"]
    if ctx["first"] and first_line(text).endswith("?"):
        think("this needs a call from the person first")
        tool("read", "lxc/profile.go")
        call_tool("ask_user", {
            "question": "Warn on every apply, or only when the profile crosses projects?",
            "options": [
                {"label": "Only across projects (recommended)", "detail": "no noise for the common case"},
                {"label": "Every apply", "detail": "simpler, but noisy"},
            ],
        })
    think("a small change, then a commit")
    tool("read", "README.md")
    with open(os.path.join(wd, "DEMO-NOTES.md"), "a", encoding="utf-8") as fh:
        fh.write("- %s\n" % (first_line(text)[:120] or "(empty)"))
    tool("edit", "DEMO-NOTES.md")
    git(wd, "add", "DEMO-NOTES.md")
    git(wd, "commit", "-qm", "chore: demo board placeholder change")
    say("Done, and committed on this session's branch.")
    usage(ctx, 18, 5200, 340)


# --------------------------------------------------------------------------
# scribe one-shots -- recognised by the prompt
# --------------------------------------------------------------------------

def scribe(ctx, prompt):
    if "ESTIMATE:" in prompt:
        return "ESTIMATE: 180"
    if "squash-merge landing commit" in prompt:
        # the engine takes a reply that is entirely one gummi-commit fence
        # the scribe is a process of its own: the prompt says whose landing
        if ctx["hero"] or HERO_MARKER in prompt.lower() or "disk-usage-percent" in prompt:
            return ("```gummi-commit\n"
                    "feat(lxc/list): add a DISK USAGE% column under -c U\n\n"
                    "- answer the capacity question lxc list was missing: memory had\n"
                    "  an absolute and a percentage column, disk only the absolute one\n"
                    "- report against the pool-reported total, not the root device\n"
                    "  quota, which is unset on most instances\n"
                    "- render a blank cell when no honest percentage exists\n"
                    "```")
        return "```gummi-commit\nchore: demo board placeholder change\n```"
    return None


# --------------------------------------------------------------------------
# protocol loop
# --------------------------------------------------------------------------

def detect(frame):
    hints = "\n".join(frame.get("hints") or [])
    return {
        "workdir": frame.get("workdir") or os.getcwd(),
        "model": frame.get("model") or "demo",
        "hero": HERO_MARKER in hints.lower(),
        "first": True,
    }


def handle(ctx, text):
    reply = scribe(ctx, text)
    if reply is not None:
        emit({"type": "text", "text": reply})
        usage(ctx, 3)
        return
    if STEP_RE.search(text) or ctx.get("step"):
        # a step's card carries its session's title, so it reads as the
        # hero's too: its brief says it is a step
        step_turn(ctx, text)
    elif ctx["hero"]:
        hero_turn(ctx, text)
    else:
        generic_turn(ctx, text)


def main():
    threading.Thread(target=reader, daemon=True).start()
    ctx = detect({})
    while True:
        frame = next_frame()
        kind = frame.get("type")
        if kind == "init":
            ctx = detect(frame)
            print("demo-agent: hero=%s" % ctx["hero"], file=sys.stderr)
        elif kind == "send":
            handle(ctx, frame.get("text") or "")
            ctx["first"] = False
            emit({"type": "idle"})
        elif kind == "interrupt":
            emit({"type": "idle"})


if __name__ == "__main__":
    try:
        main()
    except (BrokenPipeError, KeyboardInterrupt, Closed):
        pass
