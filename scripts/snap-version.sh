#!/bin/sh
# Prints the snap's version, derived from git.
#
# RELEASE_VERSION wins when set to something other than the 0.0.0
# placeholder. Otherwise the nearest tag, commit distance and short hash
# (`v1.2.0-3-gabc1234`, leading `v` dropped), or `0.0.0+git<count>.<hash>`
# when the repository has no tag yet. A dirty tree gets a `-dirty` suffix.
# Snap versions allow 32 characters of [A-Za-z0-9.+~:-].
set -eu

if [ -n "${RELEASE_VERSION:-}" ] && [ "$RELEASE_VERSION" != "0.0.0" ]; then
  printf '%s\n' "$RELEASE_VERSION"
  exit 0
fi

if ! git rev-parse --git-dir >/dev/null 2>&1; then
  echo "snap-version: not a git checkout and RELEASE_VERSION is unset" >&2
  exit 1
fi

if v=$(git describe --tags --long 2>/dev/null); then
  v=${v#v}
else
  v="0.0.0+git$(git rev-list --count HEAD).$(git rev-parse --short=7 HEAD)"
fi

if [ -n "$(git status --porcelain --untracked-files=no 2>/dev/null)" ]; then
  v="$v-dirty"
fi
printf '%s\n' "$v" | cut -c1-32
