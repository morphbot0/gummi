#!/usr/bin/env bash
# Drives the gummi demo inside tmux and captions it as it goes.
#
# vhs cannot draw a keystroke overlay, so the caption strip is tmux's own
# two-row status bar: the chapter on top, and the current narration plus
# the key about to be pressed below it. This script sends every keystroke
# itself (rather than letting vhs type), so the strip and the keypress
# stay in lockstep.
#
# Usage: scripts/demo-drive.sh <repo>     (the workspace demo-setup.sh made)
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
bin="${GUMMI_BIN:-$root/bin/gummi}"
agent="$root/scripts/demo-agent.py"
repo="${1:?usage: demo-drive.sh <repo>}"
sock="gummidemo"

# Pacing. SPEED scales every pause, so one knob retimes the whole video.
SPEED="${DEMO_SPEED:-1.0}"

BG="#1b1f2e"      # slightly lifted off the terminal so the strip reads as an overlay
FG="#d2d5e8"
DIM="#6b7290"
ACC="#8f8ff5"
KEYBG="#8f8ff5"
KEYFG="#12141c"

t() { # scaled sleep
    awk -v a="$1" -v s="$SPEED" 'BEGIN{printf "%.3f", a*s}' | xargs sleep
}

tm() { tmux -L "$sock" "$@"; }

cleanup() { tm kill-server 2>/dev/null || true; }
trap cleanup EXIT

# --------------------------------------------------------------------------
# the caption strip
# --------------------------------------------------------------------------

setup_strip() {
    tm set -g status 2
    tm set -g status-interval 1
    tm set -g status-style "bg=$BG,fg=$FG"
    tm set -g pane-border-style "fg=$BG"
    tm set -g pane-active-border-style "fg=$BG"
    tm set -g message-style "bg=$ACC,fg=$KEYFG"
    tm set -g @chapter ""
    tm set -g @note ""
    tm set -g @key ""
    # row 0: the chapter, row 1: narration on the left, keycap on the right
    tm set -g 'status-format[0]' \
        "#[bg=$BG,fg=$ACC,bold] #{@chapter}#[fg=$DIM,nobold]#{@sub}"
    tm set -g 'status-format[1]' \
        "#[bg=$BG,fg=$FG] #{?#{@note},▸ #{@note},}#[align=right]#{?#{@key},#[bg=$KEYBG#,fg=$KEYFG#,bold] #{@key} #[bg=$BG]   ,}"
}

chapter() { # chapter <title> [subtitle]
    tm set -g @chapter "$1"
    tm set -g @sub "${2:+   $2}"
    tm set -g @note ""
    tm set -g @key ""
}

say() { # say <narration> [pause]
    tm set -g @note "$1"
    tm set -g @key ""
    t "${2:-2.2}"
}

# press <tmux-key> <keycap-label> <narration> [pause-after]
press() {
    local key="$1" cap="$2" note="$3" after="${4:-1.4}"
    tm set -g @note "$note"
    tm set -g @key "$cap"
    t 1.5
    tm send-keys "$key"
    t "$after"
    tm set -g @key ""
}

# repeat <tmux-key> <keycap> <narration> <times> [gap]
repeat() {
    local key="$1" cap="$2" note="$3" n="$4" gap="${5:-0.35}"
    tm set -g @note "$note"
    tm set -g @key "$cap"
    t 1.0
    for _ in $(seq 1 "$n"); do
        tm send-keys "$key"
        t "$gap"
    done
    t 0.6
    tm set -g @key ""
}

# typeline <text> <narration> -- types it a character at a time
typeline() {
    local text="$1" note="$2" i ch
    tm set -g @note "$note"
    tm set -g @key "type"
    t 1.0
    for ((i = 0; i < ${#text}; i++)); do
        ch="${text:i:1}"
        tm send-keys -l -- "$ch"
        t 0.035
    done
    t 0.8
    tm set -g @key ""
}

# press_when <screen-regex> <key> <keycap> <narration> [wait] [after]
# Press only if the thing the key is meant for is actually on screen. A
# blind Enter lands on whatever decision is up at that moment, which is not
# always the one the caption is talking about.
press_when() {
    local re="$1" key="$2" cap="$3" note="$4" waitfor="${5:-10}" after="${6:-2.0}" i
    tm set -g @note "$note"
    tm set -g @key "$cap"
    t 1.2
    for ((i = 0; i < waitfor * 4; i++)); do
        if tm capture-pane -p 2>/dev/null | grep -qE "$re"; then
            tm send-keys "$key"
            t "$after"
            tm set -g @key ""
            return 0
        fi
        sleep 0.25
    done
    echo "demo-drive: skipped $cap -- /$re/ never appeared" >&2
    tm set -g @key ""
    return 0
}

# to_board <narration> -- esc until the backlog is actually on screen.
# One esc is not enough when a modal is stacked over the card page, and
# esc-ing one time too many from the board is harmless.
to_board() {
    local i
    tm set -g @note "$1"
    tm set -g @key "esc"
    t 1.4
    for ((i = 0; i < 4; i++)); do
        tm capture-pane -p 2>/dev/null | grep -q "BOARD " && break
        tm send-keys Escape
        t 0.7
    done
    t 1.0
    tm set -g @key ""
}

# await <regex> [timeout-seconds] -- keeps the video in step with the app
# rather than with a stopwatch. Never fatal: a timed-out cue should cost
# one awkward pause, not the rest of the take.
await() {
    local re="$1" limit="${2:-90}" i
    for ((i = 0; i < limit * 4; i++)); do
        if tm capture-pane -p 2>/dev/null | grep -qE "$re"; then
            return 0
        fi
        sleep 0.25
    done
    echo "demo-drive: timed out waiting for /$re/" >&2
    return 0
}

# --------------------------------------------------------------------------
# the scenario
# --------------------------------------------------------------------------

cues() {
    t 1.5

    # -- 1 ------------------------------------------------------------------
    chapter "1 · One board, not five terminals" "canonical/lxd"
    say "Every unit of work is a session: a coding agent on a worktree and a branch of its own." 3.6
    repeat Down "j" "j and k walk the board. There is only ever one list on screen." 3 0.7
    say "Two sessions finished their work; one is waiting on its person's answer." 3.0

    # -- 2 ------------------------------------------------------------------
    chapter "2 · A new session" "lxc list is missing a column"
    press n "n" "n starts a session. The first line is its title and its first message." 1.6
    typeline "lxc list: add a disk usage percent column" \
        "Say what you want, the way you would to a colleague."
    press Enter "enter" "Create it. The session opens on its own thread." 2.5
    await "Which denominator" 60
    say "It read lxc/list.go first, then brought the one choice that is yours." 3.4

    # A smoke run stops here: enough to prove the pipeline, cheap to redo.
    if [ -n "${DEMO_SMOKE:-}" ]; then
        chapter "gummi" "smoke run"
        say "Smoke run complete." 2.0
        return 0
    fi

    # -- 3 ------------------------------------------------------------------
    chapter "3 · Agree it, then write it down" "the session's spec"
    say "Every question comes with its recommendation attached — agree in one key." 3.2
    press_when "Which denominator" Enter "enter" "The pool total, not the quota." 15 2.0
    await "Hand it to a crew" 60
    say "It wrote what you agreed into the session's spec — with done-when items gummi can run." 3.8
    say "The work divides, so it offers a crew. A small change would not need one." 3.4

    # -- 4 ------------------------------------------------------------------
    chapter "4 · A crew, when the work is worth one" "planned, then approved by you"
    press_when "Hand it to a crew" Enter "enter" "Yes: call a crew." 15 2.0
    await "Run this crew" 60
    say "A planner turned the spec into nodes: a scout, the column, its test." 3.4
    say "Each node owns its paths. Two nodes never write the same file at once." 3.4
    say "No agent approves its own crew. That is your key to press." 3.0
    press_when "Run this crew" Enter "enter" "Run the crew." 15 2.0

    # -- 5 ------------------------------------------------------------------
    chapter "5 · The crew works" "every node checked and reviewed"
    await "t1 scout +landed" 120
    say "Each node runs on a branch of its own, forked from the crew's." 3.2
    await "rework 1" 150
    say "t2's reviewer is a fresh agent with no stake in it — and it sent t2 back." 3.8
    say "The help text never learned about U. The rework adds it; review runs again." 3.8
    await "t3 tester +(running|checking|reviewing|landed)" 150
    say "Then the tester, building on what t2 landed." 3.0
    await "crew · report" 180
    say "A final review runs the spec's done-when checks on the crew's whole branch." 3.6

    # -- 6 ------------------------------------------------------------------
    chapter "6 · The crew reports to the session" "and the session carries on"
    await "land it" 60
    say "The report comes back to the session, which tells you what it means." 3.4
    typeline "land it" "Land the crew's work on the session's branch."
    press Enter "enter" "Send it." 2.0
    await "verified at" 60
    say "One commit, and the head now says which crew verified it, at which commit." 3.8
    say "Commit on top of it later and the head says how many commits no crew checked." 3.8
    press M-d "alt+d" "alt+d reads the diff: the column, its guard, its test." 3.2
    t 2.4

    # -- 7 ------------------------------------------------------------------
    chapter "7 · Landing is yours" "a person's read of the diff"
    to_board "Back to the board."
    press_when "BOARD " m "m" "m squash-merges the session into main." 10 3.4
    await "DISK USAGE% column under -c U" 30
    say "gummi drafts the landing message from the branch's own commits…" 3.4
    say "…and then stops. You read it, edit it, land it. Nothing lands unread." 3.8
    press Escape "esc" "Not today — the branch keeps." 2.2

    chapter "gummi" "a meta-harness for coding agents"
    say "Sessions by default. A planned, reviewed crew when the work is worth one." 4.2
    t 2.0
}

# --------------------------------------------------------------------------

main() {
    [ -x "$bin" ] || { echo "gummi not built at $bin" >&2; exit 1; }
    [ -d "$repo/.gummi" ] || { echo "no .gummi in $repo -- run demo-setup.sh" >&2; exit 1; }

    cleanup
    tm new-session -d -s demo -x "${DEMO_COLS:-132}" -y "${DEMO_ROWS:-38}" \
        "cd '$repo' && GUMMI_AGENT=headless GUMMI_AGENT_CMD='$agent' \
         GUMMI_NOTIFY=off GOTOOLCHAIN=auto '$bin'"
    tm set -g window-size latest
    tm set -g aggressive-resize on
    tm set -g status-keys emacs
    setup_strip

    # DEMO_NO_ATTACH drives the session with no client attached: it proves
    # the whole cue sequence and its awaits without paying for a render.
    if [ -n "${DEMO_NO_ATTACH:-}" ]; then
        sleep 2.5
        cues
        tm kill-server 2>/dev/null || true
        echo "GUMMI-DEMO-DONE"
        return 0
    fi

    ( sleep 2.5; cues; sleep 1; tm kill-server 2>/dev/null ) &

    # The cue runner kills the server when it is finished, which makes
    # attach exit non-zero -- that is the normal ending, not a failure.
    tm attach -t demo || true

    # The recorder watches for this line to know the take is over; it is
    # deliberately not part of the command vhs types, so the sentinel can
    # never match before the run actually finishes.
    clear || true
    echo "GUMMI-DEMO-DONE"
}

main "$@"
