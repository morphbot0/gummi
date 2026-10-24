#!/usr/bin/env bash
# Runs gummi web's UX tour (e2e/web/ux): journeys through the page as a
# person takes them, at desktop, laptop and phone sizes, photographed and
# probed at every step. Writes e2e/web/ux-out/ (gitignored): INDEX.md to
# read first, then <project>/<journey>/NN-step.png and journey.json.
# Arguments pass to `playwright test`, e.g.:
#
#   scripts/web-ux.sh                       # every journey, every size
#   scripts/web-ux.sh --project=phone       # one size
#   scripts/web-ux.sh ux/triage.ux.ts       # one journey
#
# The tour asserts nothing: a step it could not take is recorded as the
# journey's "blocked" line, with a screenshot. It is not part of make ci.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
suite="$root/e2e/web"

if [ -f "$HOME/.local/pwlibs/env.sh" ]; then
    # shellcheck disable=SC1091
    . "$HOME/.local/pwlibs/env.sh"
fi
command -v node >/dev/null || { echo "web-ux: node not found (need Node 22)" >&2; exit 1; }
command -v python3 >/dev/null || { echo "web-ux: python3 not found (the scripted agent needs it)" >&2; exit 1; }
[ -x /usr/local/go/bin/go ] && PATH="/usr/local/go/bin:$PATH"
# a tour started from inside a gummi session must not look like one of its
# agents to the gummi it starts, nor sign with the session's key
unset GUMMI_SPAWNED
while IFS= read -r v; do unset "$v"; done < <(env | sed -n 's/^\(GIT_CONFIG_[A-Z_0-9]*\)=.*/\1/p')

cd "$suite"
if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then
    npm ci --no-audit --no-fund >/dev/null
fi
npx playwright install chromium-headless-shell >/dev/null

rm -rf ux-out
node ux/run.mjs "$@" || true
# node's own output may be lost under a confined node: say it from here
if [ ! -f ux-out/INDEX.md ]; then
    echo "web-ux: no INDEX.md was written; see e2e/web/ux-out/run.log" >&2
    exit 1
fi
sed -n '/^| journey/,/^$/p' ux-out/INDEX.md
echo "read e2e/web/ux-out/INDEX.md"
