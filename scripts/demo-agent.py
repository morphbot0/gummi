#!/usr/bin/env python3
"""A scripted agent for the gummi demo recording.

Speaks gummi's headless backend protocol (internal/agent/headless.go) on
stdio, so the engine, the worktrees, the session's spec, the crew's
branches, its checks and the diff are all real -- only the model's words
are pre-authored. That makes the demo deterministic and offline: every
take is identical, and a re-record after a UI change costs one command.

Wire in with:

    GUMMI_AGENT=headless GUMMI_AGENT_CMD=scripts/demo-agent.py gummi

The hero is a session titled "lxc list: add a disk usage percent column"
in a clone of canonical/lxd. Its story, turn by turn:

  session     reads lxc/list.go, asks which denominator (ask_user), writes
              a spec with four done-when items, then offers a crew
              (ask_user); on yes it calls crew_start
  planner     plans three nodes: t1 scout, t2 implementer (lxc/list.go),
              t3 tester (lxc/list_test.go), each needing the one before
  t1          reads and reports how the memory pair is built
  t2          adds the column and its shorthand entry -- but not the help
              line, so its reviewer sends it back once; the rework adds it
  t3          adds TestDiskUsagePercentColumn
  final       the final review passes; the report reaches the session
  session     explains the report; told to land it, calls crew_land

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

CREW_PLAN = """\
lanes: 2
budget: 600
nodes:
  - id: t1
    role: scout
    title: Map how the memory pair is built
    brief: Read lxc/list.go and report how -c m and -c M are built and registered, so t2 mirrors them.
    tier: cheap
    envelope: 60
  - id: t2
    role: implementer
    title: Add the DISK USAGE% column under -c U
    brief: The column function, its shorthand entry, and the help line, per the spec's Approach.
    needs: [t1]
    owns: [lxc/list.go]
    serves: [DW-1, DW-2, DW-4]
    checks: ["go build ./lxc/...", "go vet ./lxc/"]
    tier: standard
    envelope: 200
  - id: t3
    role: tester
    title: Test the column, Total == 0 included
    brief: A table test of diskUsagePercentColumnData in lxc/list_test.go.
    needs: [t2]
    owns: [lxc/list_test.go]
    serves: [DW-3]
    checks: ["go test ./lxc/ -run TestDiskUsagePercentColumn"]
    tier: cheap
    envelope: 120
"""

# --------------------------------------------------------------------------
# the change the crew makes to the real lxd tree
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
        "This divides cleanly: read the memory pair, add the column, test it.")
    usage(ctx, 44, 11800, 920)
    answer = call_tool("ask_user", {
        "question": "Hand it to a crew? A planner splits it into nodes, each reviewed by a fresh agent.",
        "options": [
            {"label": "Call a crew (recommended)",
             "detail": "you approve its plan before anything runs"},
            {"label": "Keep it here", "detail": "I make the change in this session"},
        ],
    })
    if "keep" in answer.lower():
        say("Then I'll do it here.")
        return
    result = call_tool("crew_start", {
        "brief": "Mirror the memory pair; the blank-cell rule matters more than the column.",
        "budget": 600,
    })
    say("Asked for a crew: %s" % first_line(result))
    usage(ctx, 12, 3100, 160)


def hero_report(ctx):
    think("reading the crew's report against the spec")
    say("The crew finished and its final review passed. All three checks ran\n"
        "on the crew's branch -- build, vet and the new test -- and the\n"
        "reviewer judged DW-4 against the combined diff.\n\n"
        "Worth knowing: t2 was sent back once, because its first commit left\n"
        "`U` out of `lxc list --help`. Say \"land it\" and I'll put it on this\n"
        "branch.")
    usage(ctx, 22, 8400, 300)


def hero_land(ctx):
    result = call_tool("crew_land", {})
    say("Landed: %s" % first_line(result))
    usage(ctx, 6, 2000, 80)


def hero_turn(ctx, text):
    low = text.lower()
    if text.startswith("The crew has reported"):
        hero_report(ctx)
    elif "land" in low and len(low) < 80:
        hero_land(ctx)
    elif ctx["first"]:
        hero_kickoff(ctx)
    else:
        say("Noted: %s" % first_line(text)[:200])
        usage(ctx, 4)


# --------------------------------------------------------------------------
# the crew
# --------------------------------------------------------------------------

def planner_turn(ctx, text):
    if "Write the plan now" not in text:
        say("Nothing to change in the plan.")
        usage(ctx, 4)
        return
    think("three pieces, each building on the one before")
    call_tool("crew_doc", {"section": "Done when"})
    result = call_tool("plan_write", {"plan": CREW_PLAN})
    say("Planned three nodes: a scout, the column, its test. (%s)" % first_line(result))
    usage(ctx, 30, 9800, 700)


def node_turn(ctx, text):
    wd = ctx["workdir"]
    m = re.search(r"You are node (t\d+)", text)
    node = m.group(1) if m else ctx.get("node", "")
    ctx["node"] = node
    if text.startswith("Your work was sent back"):
        think("the reviewer is right: the help text never learned about U")
        patch_list_go(wd, with_help=True)
        tool("edit", "lxc/list.go")
        git(wd, "commit", "-qam", "lxc/list: list U beside D in the help text")
        tool("bash", "git commit")
        say("Added `U - Disk usage (%)` under `D - disk usage`.\n"
            "DONE: listed U in lxc list --help\n"
            "DECISION: kept it beside D so the disk pair reads together")
        usage(ctx, 18, 6000, 240)
    elif node == "t1":
        tool("read", "lxc/list.go")
        tool("grep", "memoryUsagePercentColumnData")
        say("`-c M` is `memoryUsagePercentColumnData`, registered as `'M'` in\n"
            "`columnsShorthandMap`; it renders blank when `limits.memory` is unset.\n"
            "DONE: mapped the memory pair for t2\n"
            "DECISION: none")
        usage(ctx, 14, 7000, 300)
    elif node == "t2":
        think("the column function below diskUsageColumnData, then its entry")
        patch_list_go(wd, with_help=False)
        tool("edit", "lxc/list.go")
        tool("bash", "go build ./lxc/...")
        git(wd, "commit", "-qam", "lxc/list: add a DISK USAGE% column under -c U")
        tool("bash", "git commit")
        say("Added `diskUsagePercentColumnData` and registered it as `'U'`.\n"
            "DONE: the column and its shorthand entry\n"
            "DECISION: a blank cell when Total is 0, as the spec says")
        usage(ctx, 52, 16000, 1400)
    elif node == "t3":
        think("a table test: half full, whole pool, no usage, no state")
        patch_list_test_go(wd)
        tool("edit", "lxc/list_test.go")
        tool("bash", "go test ./lxc/ -run TestDiskUsagePercentColumn")
        git(wd, "commit", "-qam", "lxc/list: test the DISK USAGE% column")
        tool("bash", "git commit")
        say("Added `TestDiskUsagePercentColumn`, Total == 0 included.\n"
            "DONE: the column's table test\n"
            "DECISION: none")
        usage(ctx, 26, 9000, 700)
    else:
        say("DONE: nothing to do\nDECISION: none")
        usage(ctx, 4)


def reviewer_turn(ctx, text):
    think("fresh context: the brief, the spec, the diff")
    tool("read", "the change")
    if text.startswith("Review the whole of crew"):
        say("The three nodes add up to the spec. `lxc list --help` lists `U`\n"
            "beside `D`, and `defaultColumns` is untouched, so bare `lxc list`\n"
            "output is unchanged: DW-4 holds.\n"
            "VERDICT: pass")
    elif "Review node t2" in text and HELP_LINE.strip() not in text:
        say("The column is right, and the blank-cell guard is commented.\n"
            "But DW-4 needs `U` in `lxc list --help`, and the diff never adds it.\n"
            "FINDING: lxc list --help does not list U - Disk usage (%)\n"
            "VERDICT: changes")
    else:
        say("Read the change against its brief. Nothing blocking.\nVERDICT: pass")
    usage(ctx, 20, 12000, 400)


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
        "planner": "You are a crew's planner" in hints,
        "reviewer": "You review one piece of a crew's work" in hints,
        "first": True,
    }


def handle(ctx, text):
    reply = scribe(ctx, text)
    if reply is not None:
        emit({"type": "text", "text": reply})
        usage(ctx, 3)
        return
    if ctx["planner"]:
        planner_turn(ctx, text)
    elif ctx["reviewer"]:
        reviewer_turn(ctx, text)
    elif re.search(r"You are node t\d+ of a crew", text) or text.startswith("Your work was sent back"):
        node_turn(ctx, text)
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
            print("demo-agent: hero=%s planner=%s reviewer=%s" %
                  (ctx["hero"], ctx["planner"], ctx["reviewer"]), file=sys.stderr)
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
