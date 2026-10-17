#!/usr/bin/env bash
# Print README.md as it should read on jophira/weft-releases for one tag.
#
# The releases repo carries no source, so a relative link such as [LICENSE](LICENSE)
# would 404 there. Each relative link is pointed at the file in jophira/weft at the
# tag being released. Absolute links and in-page anchors are left alone.
#
# Usage: scripts/release-readme.sh v0.3.0 [README.md]
set -euo pipefail

tag="${1:?usage: release-readme.sh <tag> [readme]}"
readme="${2:-README.md}"

sed -E "s#\]\(([^):#]+)\)#](https://github.com/jophira/weft/blob/${tag}/\1)#g" "$readme"
