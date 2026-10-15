#!/usr/bin/env bash
# Fast local Go tests: internal/cli in concurrent shards plus the provider packages.
#   scripts/test-fast.sh                      all of it
#   scripts/test-fast.sh ./internal/providers/namespace   only the given packages
# Env: SHARDS (default min(nproc, 8)), FRESH=1 (-count=1, bypass test cache).
set -uo pipefail
cd "$(dirname "$0")/.."
umask 022 # tests fail under 0002

table=scripts/test-fast-durations.txt
flags=(-timeout=20m)
[[ -n "${FRESH:-}" ]] && flags+=(-count=1)
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
names=()
pids=()
start=$(date +%s)

run() { # name, go test args...
  local name=$1 s=$(date +%s) rc
  shift
  go test "${flags[@]}" "$@" >"$out/$name.log" 2>&1
  rc=$?
  printf '%s\t%s\t%ss\n' "$name" "$rc" "$(($(date +%s) - s))" >"$out/$name.res"
  return $rc
}
launch() {
  names+=("$1")
  run "$@" &
  pids+=($!)
}

if (($#)); then
  launch pkgs "$@"
else
  n=${SHARDS:-$(($(nproc) < 8 ? $(nproc) : 8))}
  go test ./internal/cli -list . | grep -E '^(Test|Example|Fuzz)' | sort >"$out/names" || exit 1
  # Longest-first onto the least loaded shard for tests in the table; the rest round-robin.
  python3 -I - "$n" "$table" "$out" <<'PY' || exit 1
import heapq, sys
n, table, out = int(sys.argv[1]), sys.argv[2], sys.argv[3]
d = {}
for line in open(table):
    if line.strip() and not line.startswith("#"):
        name, secs = line.split()
        d[name] = float(secs)
names = [x.strip() for x in open(out + "/names") if x.strip()]
shards = [[] for _ in range(n)]
heap = [(0.0, i) for i in range(n)]
for t in sorted((t for t in names if t in d), key=lambda t: -d[t]):
    load, i = heapq.heappop(heap)
    shards[i].append(t)
    heapq.heappush(heap, (load + d[t], i))
for k, t in enumerate(t for t in names if t not in d):
    shards[k % n].append(t)
for i, l in enumerate(shards):
    open(f"{out}/shard{i}", "w").write("|".join(l))
PY
  launch providers ./internal/providers/...
  for ((i = 0; i < n; i++)); do
    [[ -s "$out/shard$i" ]] && launch "cli-$i" ./internal/cli -run "^($(<"$out/shard$i"))\$"
  done
fi

fail=0
for p in "${pids[@]}"; do wait "$p" || fail=1; done
for name in "${names[@]}"; do
  IFS=$'\t' read -r _ rc secs <"$out/$name.res"
  printf '%-10s rc=%s %s\n' "$name" "$rc" "$secs"
  if [[ $rc != 0 ]]; then cat "$out/$name.log"; fi
done
printf 'total %ss\n' "$(($(date +%s) - start))"
exit $fail
