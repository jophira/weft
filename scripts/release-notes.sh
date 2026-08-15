#!/usr/bin/env bash
# Extract one version's section from CHANGELOG.md, for goreleaser --release-notes.
#
# The published release notes and the changelog are the same curated text. The
# alternative, goreleaser generating notes from commit subjects, cannot carry a
# reason or a migration note and silently drops docs:, test: and chore: work.
#
# Usage: scripts/release-notes.sh v0.2.0 [CHANGELOG.md]
#
# Exits non-zero when the section is missing or empty, so a release fails loudly
# rather than publishing blank notes. That is the whole point of the guard: an
# empty release page is not obviously wrong to anyone watching the job go green.
set -euo pipefail

tag="${1:?usage: release-notes.sh <tag> [changelog]}"
changelog="${2:-CHANGELOG.md}"
version="${tag#v}"

if [ ! -f "$changelog" ]; then
    echo "release-notes: $changelog not found" >&2
    exit 1
fi

# Print the lines between this version's heading and the next version heading.
# Matching on "## [x.y.z]" keeps the heading itself out of the notes, since
# GitHub already titles the release with the tag.
notes=$(awk -v v="$version" '
    $0 ~ "^## \\[" v "\\]" { inside = 1; next }
    inside && /^## \[/     { exit }
    inside                 { print }
' "$changelog")

# Trim leading and trailing blank lines.
notes=$(printf '%s\n' "$notes" | sed -e '/./,$!d' | sed -e :a -e '/^\n*$/{$d;N;};/\n$/ba')

if [ -z "$notes" ]; then
    echo "release-notes: no entries under '## [$version]' in $changelog" >&2
    echo "release-notes: add a section for this version before tagging" >&2
    exit 1
fi

printf '%s\n' "$notes"
