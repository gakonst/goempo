#!/usr/bin/env bash
# Snapshot a live goempo state datadir (one that replay -datadir or goempo is
# writing) into a new directory by hard-linking the segments its MANIFEST
# names. Segments are immutable once written and new ones get fresh names, so
# the snapshot is the source's state at its last flush and writing to either
# directory never changes the other.
#   scripts/node-snapshot.sh SRC DST
set -euo pipefail
src=$1
dst=$2
if [ -e "$dst" ]; then
  echo "node-snapshot: $dst exists" >&2
  exit 1
fi
for attempt in 1 2 3 4 5; do
  tmp=$(mktemp -d "$dst.tmp.XXXX")
  cp "$src/MANIFEST" "$tmp/MANIFEST"
  ok=1
  for seg in $(grep '\.seg$' "$tmp/MANIFEST"); do
    if ! ln "$src/$seg" "$tmp/$seg" 2>/dev/null; then
      ok=0
      break
    fi
  done
  if [ $ok = 1 ] && cmp -s "$src/MANIFEST" "$tmp/MANIFEST"; then
    mv "$tmp" "$dst"
    echo "node-snapshot: $dst ($(grep -c '\.seg$' "$dst/MANIFEST") segments)"
    exit 0
  fi
  rm -rf "$tmp"
  echo "node-snapshot: $src changed during attempt $attempt, retrying" >&2
  sleep 2
done
echo "node-snapshot: gave up" >&2
exit 1
