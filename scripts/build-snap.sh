#!/bin/bash
set -euo pipefail

orig_pwd="$(pwd)"
workdir="$(mktemp -d -p "${TMPDIR:-/tmp}" gummi-build.XXXXXX)"
used_destructive=0

cleanup() {
  if [ "$used_destructive" = "1" ]; then
    sudo -E snapcraft clean --destructive-mode || true
    sudo -E rm -rf "$workdir" || true
  else
    snapcraft clean || true
    rm -rf "$workdir" || true
  fi
}
trap cleanup EXIT

# Copy HEAD into a tempdir as a clone, not an archive: the snap's version is
# derived from git, so the build tree needs the history and tags.
git clone -q --no-hardlinks "$orig_pwd" "$workdir/src"

if [ ! -f "$workdir/src/snap/snapcraft.yaml" ]; then
  echo "expected $workdir/src/snap/snapcraft.yaml to exist" >&2
  exit 1
fi

# Build from the repo root, not from snap/: the parts' `source: .` has to
# resolve inside the project directory snapcraft mounts.
cd "$workdir/src"

if systemd-detect-virt --container --quiet; then
  used_destructive=1
  sudo -E snapcraft --destructive-mode
else
  used_destructive=0
  snapcraft
fi

mkdir -p "$orig_pwd/results"
rm -f "$orig_pwd/results/"*.snap

snapfile=$(ls *.snap 2>/dev/null | head -n1 || true)
if [ -z "$snapfile" ]; then
  echo "snapcraft pack failed: no .snap produced" >&2
  exit 1
fi

cp "$snapfile" "$orig_pwd/results/"
echo "Packed snap copied to results/"
