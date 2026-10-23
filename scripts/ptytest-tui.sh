#!/usr/bin/env bash
# Interactive pty test for the TUI's interaction model: drives a real
# gummi binary inside tmux and asserts on what the pane actually shows.
# Covers the things unit and golden tests structurally cannot — focus
# transitions across surfaces, dialogs that open other dialogs, and
# whether the program is still alive after a keystroke. It caught a
# panic on `?` then `esc` that a full static review had passed as clean.
#
#   scripts/ptytest-tui.sh
set -uo pipefail

# Requires tmux and git. Builds its own binary and workspace in a temp dir.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SC="$(mktemp -d)"
trap 'tmux kill-session -t ptytest 2>/dev/null; rm -rf "$SC"' EXIT
BIN="$SC/gummi"
WS="$SC/ws"
S=ptytest

command -v tmux >/dev/null || { echo "ptytest: tmux is required"; exit 2; }
echo "building gummi…"
go build -C "$ROOT" -o "$BIN" ./cmd/gummi || exit 2
PASS=0; FAIL=0; FAILED_NAMES=()

send() { tmux send-keys -t $S "$@"; sleep 0.25; }

# expect <name> <regex> — poll the pane until it matches, else fail.
expect() {
  local name="$1" re="$2" i
  for i in $(seq 1 24); do
    if pane | grep -qE -- "$re"; then
      PASS=$((PASS+1)); printf '  ok   %s\n' "$name"; return 0
    fi
    sleep 0.25
  done
  FAIL=$((FAIL+1)); FAILED_NAMES+=("$name")
  printf '  FAIL %s   (no match for: %s)\n' "$name" "$re"
  pane | sed 's/^/       | /' | head -30
  return 1
}

# refute <name> <regex> — pane must NOT match (checked after settling).
refute() {
  local name="$1" re="$2"
  sleep 0.4
  if pane | grep -qE -- "$re"; then
    FAIL=$((FAIL+1)); FAILED_NAMES+=("$name")
    printf '  FAIL %s   (unexpectedly matched: %s)\n' "$name" "$re"
    pane | sed 's/^/       | /' | head -30
    return 1
  fi
  PASS=$((PASS+1)); printf '  ok   %s\n' "$name"; return 0
}

alive() {
  local name="$1"
  if tmux has-session -t $S 2>/dev/null; then
    PASS=$((PASS+1)); printf '  ok   %s\n' "$name"
  else
    FAIL=$((FAIL+1)); FAILED_NAMES+=("$name")
    printf '  FAIL %s   (the TUI died — crash?)\n' "$name"
  fi
}

section() { printf '\n== %s ==\n' "$1"; }

# ---------------------------------------------------------------- setup
rm -rf "$WS"; mkdir -p "$WS/repo-b" "$WS/repo-c"
git -C "$WS" init -q -b main
git -C "$WS" config user.name t; git -C "$WS" config user.email t@e.invalid
echo x > "$WS/README.md"; git -C "$WS" add README.md; git -C "$WS" commit -q -m init
for r in repo-b repo-c; do
  git -C "$WS/$r" init -q -b main
  git -C "$WS/$r" config user.name t; git -C "$WS/$r" config user.email t@e.invalid
  echo y > "$WS/$r/README.md"; git -C "$WS/$r" add README.md; git -C "$WS/$r" commit -q -m init
done
( cd "$WS" && "$BIN" init >/dev/null 2>&1 )
printf '\nrepos:\n  b: repo-b\n  c: repo-c\n' >> "$WS/.gummi/config.yaml"

pane() { tmux capture-pane -t $S -p 2>/dev/null; }
lit()  { tmux send-keys -t $S -l "$1"; sleep 0.35; }
clear_composer() { tmux send-keys -t $S C-u; sleep 0.3; }

# tab_to_buttons — press Tab until the new-card form's button row has the
# focus (the options rows in between vary with what the form offers).
tab_to_buttons() {
  local i
  for i in $(seq 1 10); do
    pane | grep -q 'enter activate' && return 0
    send Tab
  done
  return 1
}

tmux kill-session -t $S 2>/dev/null
tmux new-session -d -s $S -x 120 -y 40 \
  "cd '$WS' && GUMMI_AGENT=headless GUMMI_AGENT_CMD='$ROOT/scripts/web-e2e-agent.py' '$BIN'"
sleep 1.8
# first start opens the agent-tab CLI picker over the board; dismiss it.
send Escape
sleep 0.5

section "1. empty board / splash"
expect "splash advertises the command menu"          'space commands'
send 'Space'
expect "menu footer appears"                         '↑↓ move'
expect "menu lists New session"                      'New session'
send Escape

section "2. create a session through the menu and the form's buttons only"
send 'Space'
lit 'sess'
expect "typing filters the menu"        'New session'
refute "filter excludes non-matches"    'Quit gummi'
send Enter
expect "menu ran New session"           'new card'
expect "repo field renders first"       'repo +choose one'
send '1'
expect "1 chose the first repo"         'repo +1 ▸ b'
lit 'pty smoke card'
tab_to_buttons || true
expect "tab reaches the button row"     '\[ Cancel \]'
expect "button-row hint replaces the field hint" 'enter activate'
send Right
send Enter
expect "Create minted the session and opened it" 'FF-001 · pty smoke card'
alive  "still alive after create"

section "3. the card page a new session opens on"
expect "the breadcrumb leads back to the board" '‹ esc board'
expect "the masthead names the branch"  'ff/pty-smoke-card'
expect "the composer is present"        '┃ '
alive  "still alive on the card page"

section "4. the composer takes the keyboard immediately"
clear_composer
lit 'test text'
expect "typing enters the composer"     '┃ test text'
clear_composer
refute "ctrl+u clears the composer"     '┃ test text'

section "5. esc leaves the card page and keeps the draft"
lit 'draft text here'
send Escape
expect "esc returned to the board"      'BOARD'
expect "the board lists the session"    'Session +pty smoke card'

section "6. duplicate has no accelerator; y is inert on the board"
send 'y'
refute "y no longer opens duplicate"    'duplicate'
alive  "still alive after y"

section "7. reopening the card from the board"
send Enter
expect "the draft survived"             '┃ draft text here'
expect "masthead shows the card id"     'FF-001 · pty smoke card'
clear_composer
alive  "still alive after reopening"

section "8. command menu: a filter that matches nothing says so"
send Escape
send 'Space'
lit 'ingest'
expect "a retired command is not listed" 'no commands match'
send Escape
refute "esc closed the menu"            '↑↓ move'
alive  "still alive after the menu"

section "9. help overlay is reachable and honest"
send '?'
expect "help opens"                     'keys · board'
expect "help lists the menu key"        'open the command menu'
# with a card on the board the table outgrows the pane, so it scrolls
send PageDown; send PageDown
expect "help scrolls to its last row"   'q +quit'
refute "help no longer advertises y"    'y +duplicate'
send Escape
alive  "esc out of help does not panic"
refute "help closed"                    'keys · board'

section "10. ctrl+c is hoisted above the overlay"
send 'Space'
expect "menu open"                      'commands'
send C-c
sleep 0.8
if tmux has-session -t $S 2>/dev/null; then
  FAIL=$((FAIL+1)); FAILED_NAMES+=("ctrl+c quits through an open dialog")
  printf '  FAIL ctrl+c quits through an open dialog (session still alive)\n'
else
  PASS=$((PASS+1)); printf '  ok   ctrl+c quits through an open dialog\n'
fi

# ---------------------------------------------------------------- note
# sections testing duplicate, delete, envelope flows through the old
# focusable action list were retired with that surface. they are
# unreachable from this suite until another agent lands the change to
# open the action inventory when Up is pressed past the top option on a
# card page with an empty composer. restore them against the inventory
# overlay then.

# ---------------------------------------------------------------- report
printf '\n================ %d passed, %d failed ================\n' "$PASS" "$FAIL"
if [ "$FAIL" -gt 0 ]; then
  printf 'failed:\n'; printf '  - %s\n' "${FAILED_NAMES[@]}"
fi
tmux kill-session -t $S 2>/dev/null
exit $([ "$FAIL" -eq 0 ] && echo 0 || echo 1)
