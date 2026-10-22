#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "$0")/.."
umask 022

usage() {
  cat <<'EOF'
usage: scripts/test-changed.sh [BASE]
Runs go test only for packages changed since BASE (default fork/moor/main, origin/main, main)
and every package whose test binary depends on one of them.
Env: RUN=regex (-run for every package), FRESH=1 (-count=1), RACE=1 (-race), LIST=1 (print, do not run).
EOF
}
[[ ${1:-} == -h || ${1:-} == --help ]] && { usage; exit 0; }

base=${1:-}
if [[ -z $base ]]; then
  for ref in fork/moor/main origin/main main; do
    git rev-parse --verify --quiet "$ref" >/dev/null && base=$ref && break
  done
fi
[[ -n $base ]] || { echo "test-changed: no base ref found" >&2; exit 2; }
mergebase=$(git merge-base "$base" HEAD) || { echo "test-changed: no merge base with $base" >&2; exit 2; }

changed=$({ git diff --name-only "$mergebase"; git ls-files --others --exclude-standard; } | sort -u)

if grep -qxE 'go\.(mod|sum)' <<<"$changed"; then
  pkgs=$(go list ./...)
else
  owners=$(go list -f '{{.ImportPath}} {{.Dir}}' ./... | CHANGED=$changed python3 -I -c '
import os, sys
root = os.getcwd()
dirs = {}
for line in sys.stdin.read().splitlines():
    pkg, d = line.split(" ", 1)
    dirs[os.path.relpath(d, root)] = pkg
for path in os.environ["CHANGED"].splitlines():
    d = os.path.dirname(path) or "."
    while d not in dirs and d != ".":
        d = os.path.dirname(d) or "."
    if d in dirs:
        print(dirs[d])
' | sort -u)
  if [[ -z $owners ]]; then
    echo "test-changed: no Go package affected since $base"
    exit 0
  fi
  pkgs=$(go list -test -f '{{if .ForTest}}{{else}}{{.ImportPath}} {{join .Deps " "}}{{end}}' ./... 2>/dev/null |
    awk -v owners="$owners" '
      BEGIN { n = split(owners, o, "\n"); for (i = 1; i <= n; i++) want[o[i]] = 1 }
      $1 !~ /\.test$/ { next }
      { pkg = $1; sub(/\.test$/, "", pkg); for (i = 2; i <= NF; i++) if ($i in want) { print pkg; next } }
    ' | sort -u)
  pkgs=$(printf '%s\n%s\n' "$owners" "$pkgs" | grep -v '^$' | sort -u)
fi

if [[ -z $pkgs ]]; then
  echo "test-changed: no Go package affected since $base"
  exit 0
fi
echo "test-changed: base=$base, $(wc -l <<<"$pkgs") package(s)"
echo "$pkgs"
[[ -n ${LIST:-} ]] && exit 0

flags=(-timeout=20m)
[[ -n ${FRESH:-} ]] && flags+=(-count=1)
[[ -n ${RACE:-} ]] && flags+=(-race)
[[ -n ${RUN:-} ]] && flags+=(-run "$RUN")
start=$(date +%s)
go test "${flags[@]}" $pkgs
rc=$?
echo "test-changed: $(($(date +%s) - start))s rc=$rc"
exit $rc
