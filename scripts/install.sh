#!/bin/sh
# Builds hb with its full version and installs it into Go's bin folder
# (GOBIN, or GOPATH/bin), which should be on PATH.
#   version: <VERSION>+<UTC build date>.<commit count>.g<short sha>
#   e.g.     0.1.0+20261115.12.g1a2b3c4
set -eu
cd "$(dirname "$0")/.."
base=$(tr -d ' \r\n' < VERSION)
case "$base" in
  *[!0-9.]* | *..* | .* | *.) echo "VERSION must be <major>.<minor>.<patch>, got '$base'" >&2; exit 1 ;;
esac
[ "$(printf '%s' "$base" | tr -cd . | wc -c)" -eq 2 ] || { echo "VERSION must be <major>.<minor>.<patch>, got '$base'" >&2; exit 1; }
count=$(git rev-list --count HEAD 2>/dev/null) || { echo "no commits yet: commit first, the version names one" >&2; exit 1; }
sha=$(git rev-parse --short HEAD)
date=$(date -u +%Y%m%d)
version="$base+$date.$count.g$sha"
if [ -n "$(git status --porcelain)" ]; then
  echo "warning: uncommitted changes: $version names g$sha, but the build includes them" >&2
fi
go install -trimpath -ldflags "-s -w -X hb/internal/cmd.Version=$version" ./cmd/hb
bin=$(go env GOBIN)
[ -n "$bin" ] || bin="$(go env GOPATH)/bin"
echo "installed $version -> $bin/hb"
