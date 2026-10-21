#!/usr/bin/env bash
# End-to-end integration check: drives the real TUI in a tmux PTY
# against a scripted demo repo, and asserts a session's create, its
# worktree and its delete on disk. The session's agent is the scripted
# fake the web suite uses, never a real backend. Exits non-zero on any
# failed assertion.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
bin="$root/bin/gummi"
sock="gummi-e2e-$$"
dir="$(mktemp -d "${TMPDIR:-/tmp}/gummi-e2e.XXXXXX")"

cleanup() {
    tmux -L "$sock" kill-server 2>/dev/null || true
    rm -rf "$dir"
}
trap cleanup EXIT

fail() { echo "e2e FAIL: $*" >&2; exit 1; }

[ -x "$bin" ] || fail "bin/gummi not built"
"$root/scripts/demo.sh" "$dir" >/dev/null

k() { tmux -L "$sock" send-keys "$@"; sleep 0.3; }
pane() { tmux -L "$sock" capture-pane -p; }

# wait (bounded) until the pane shows a marker string; on a miss, print
# what the pane shows instead
await() {
    for _ in $(seq 1 50); do
        pane | grep -q "$1" && return 0
        sleep 0.2
    done
    pane >&2
    fail "timed out waiting for: $1"
}

tmux -L "$sock" new-session -d -x 120 -y 34 \
    "cd '$dir' && GUMMI_AGENT=headless GUMMI_AGENT_CMD='$root/scripts/web-e2e-agent.py' '$bin'"
await "no cards yet"

# create a session through the real form
k n; k 'Demo session'; k Enter
await "“Demo session” created"
[ -f "$dir/.gummi/state/gummi.db" ] || fail "state db missing"

# a session works in a worktree of its own from the moment it exists
tree="$dir/.gummi/worktrees/FF-001"
for _ in $(seq 1 50); do [ -d "$tree" ] && break; sleep 0.2; done
[ -d "$tree" ] || fail "the session's worktree was not created"

# delete from the board (creating opened the session's thread): confirm
# dialog -> record and worktree gone
k Escape
k D; await "delete “Demo session”"
k y
await "no cards yet"
[ ! -d "$tree" ] || fail "worktree survived delete"

k q
echo "e2e PASS"
