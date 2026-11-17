#!/usr/bin/env bash
# Print goempo's current numbers: repository size, the frontier ladder,
# replay throughput from cached results, and (with --run) a fresh measurement
# of the rungs that score in seconds from the local caches.
#
#   scripts/metrics.sh            repo counts, frontier rows, cached results
#   scripts/metrics.sh --run      also score R1, R2, R5 and a short R3 replay
#   scripts/metrics.sh --all      --run plus the R6 simulator tests (~25 s)
#   scripts/metrics.sh --json     one JSON object instead of text (implies --run)
#
# Environment: GOEMPO_CACHE (default ~/cache), GOEMPO_RESULTS (space-separated
# directories searched for replay -result JSON files, default "~/scratch ~/cache
# ~/goempo-runs"), GOEMPO_REPLAY_TO (last block of the --run replay, default 1000000).
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"

run=0; sim=0; json=0
for arg in "$@"; do
  case "$arg" in
    --run) run=1 ;;
    --all) run=1; sim=1 ;;
    --json) run=1; json=1 ;;
    -h|--help) sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

cache="${GOEMPO_CACHE:-$HOME/cache}"
results_dirs="${GOEMPO_RESULTS:-$HOME/scratch $HOME/cache $HOME/goempo-runs}"
replay_to="${GOEMPO_REPLAY_TO:-1000000}"
have_jq=0; command -v jq >/dev/null && have_jq=1

out() { [ "$json" = 1 ] || printf '%s\n' "$*"; }
kv() { [ "$json" = 1 ] || printf '  %-28s %s\n' "$1" "$2"; }
declare -A J

# ---- repository ------------------------------------------------------------
commits=$(git rev-list --count HEAD)
head=$(git rev-parse --short HEAD)
first=$(git log --max-parents=0 --format=%aI | tail -1)
last=$(git log -1 --format=%aI)
span_h=$(( ( $(date -d "$last" +%s) - $(date -d "$first" +%s) ) / 3600 ))
types=$(git log --format=%s | sed -E 's/^([a-z]+)(\([^)]*\))?!?:.*/\1/; t; s/.*/other/' | sort | uniq -c | sort -rn | awk '{printf "%s %s, ", $2, $1}' | sed 's/, $//')

mapfile -t gofiles < <(git ls-files '*.go' | grep -v '/testdata/')
prod_files=(); test_files=()
for f in "${gofiles[@]}"; do
  case "$f" in *_test.go) test_files+=("$f") ;; *) prod_files+=("$f") ;; esac
done
lines() { if [ $# -eq 0 ]; then echo 0; else cat "$@" | wc -l; fi; }
prod_lines=$(lines "${prod_files[@]}")
test_lines=$(lines "${test_files[@]}")
if command -v go >/dev/null; then packages=$(go list ./... | wc -l); else packages=$(printf '%s\n' "${prod_files[@]}" | xargs -n1 dirname | sort -u | wc -l); fi
count_funcs() { if [ ${#test_files[@]} -eq 0 ]; then echo 0; else cat "${test_files[@]}" | grep -c "^func $1" || true; fi; }
tests=$(count_funcs 'Test[A-Z_]')
fuzz=$(count_funcs 'Fuzz[A-Z_]')
benches=$(count_funcs 'Benchmark[A-Z_]')
fixtures=$(git ls-files | grep -c '/testdata/' || true)
fixture_kb=$(( $(git ls-files -z | grep -z '/testdata/' | xargs -0 cat 2>/dev/null | wc -c) / 1024 ))
deviations=$(grep -cE '^## [0-9]+' docs/DEVIATIONS.md 2>/dev/null || true)

out "goempo metrics at $head ($(date -u +%Y-%m-%dT%H:%MZ))"
out ""
out "Repository"
kv "commits" "$commits ($first .. $last, ${span_h}h)"
kv "by type" "$types"
kv "Go lines (production)" "$prod_lines in ${#prod_files[@]} files"
kv "Go lines (tests)" "$test_lines in ${#test_files[@]} files"
kv "packages" "$packages"
kv "tests / fuzz / benchmarks" "$tests / $fuzz / $benches"
kv "committed fixtures" "$fixtures files, ${fixture_kb} KB"
kv "recorded deviations" "$deviations"
J[commits]=$commits; J[head]="\"$head\""; J[prod_lines]=$prod_lines; J[test_lines]=$test_lines
J[packages]=$packages; J[tests]=$tests; J[fuzz]=$fuzz; J[benchmarks]=$benches; J[fixtures]=$fixtures

# ---- frontier ----------------------------------------------------------------
out ""
out "Frontier (FRONTIER.md table)"
frontier_rows=$(awk -F'|' '/^\| R[0-9]/ { gsub(/^ +| +$/, "", $2); gsub(/^ +| +$/, "", $3); gsub(/^ +| +$/, "", $4); print $2 "\t" $3 "\t" $4 }' FRONTIER.md)
while IFS=$'\t' read -r rung score commit; do
  [ -n "$rung" ] || continue
  short="$score"; [ ${#short} -gt 96 ] && short="${short:0:93}..."
  kv "$rung" "$short [$commit]"
done <<< "$frontier_rows"
if [ "$have_jq" = 1 ] && [ -s FRONTIER.jsonl ]; then
  out ""
  out "Frontier (last FRONTIER.jsonl row per rung)"
  while IFS=$'\t' read -r rung score commit date; do
    short="$score"; [ ${#short} -gt 96 ] && short="${short:0:93}..."
    kv "$rung" "$short [$commit $date]"
  done < <(jq -rs 'group_by(.rung) | map(last) | .[] | [.rung, .score, .commit, .date] | @tsv' FRONTIER.jsonl)
  J[frontier]=$(jq -cs 'group_by(.rung) | map(last) | map({key: .rung, value: {score, commit, date}}) | from_entries' FRONTIER.jsonl)
  J[frontier_rows]=$(wc -l < FRONTIER.jsonl)
fi

# ---- caches ------------------------------------------------------------------
out ""
out "Local caches ($cache)"
if [ -d "$cache/mainnet" ]; then
  chunks=$(ls "$cache/mainnet" | grep -cE '^[0-9]{10}\.blkz?$' || true)
  last_chunk=$(ls "$cache/mainnet" | grep -E '^[0-9]{10}\.blkz?$' | sort | tail -1 | sed -E 's/\..*//')
  cached_to=$(( 10#${last_chunk:-0} + 9999 ))
  kv "mainnet block chunks" "$chunks (10k blocks each, through block $cached_to, $(du -sh "$cache/mainnet" | cut -f1))"
  J[cached_height]=$cached_to
fi
if [ -d "$cache/rpc" ]; then
  corpus=$(cat "$cache"/rpc/corpus*.jsonl 2>/dev/null | wc -l)
  kv "RPC corpus requests" "$corpus ($cache/rpc/corpus*.jsonl)"
fi
[ -f "$cache/blocks/blocks.jsonl" ] && kv "R1 sample blocks" "$(wc -l < "$cache/blocks/blocks.jsonl") ($cache/blocks/blocks.jsonl)"

if [ "$have_jq" = 1 ]; then
  out ""
  out "Replay throughput (cached replay -result files, longest runs first)"
  best=""
  for d in $results_dirs; do
    [ -d "$d" ] || continue
    for f in "$d"/*.json; do
      [ -f "$f" ] || continue
      line=$(jq -r 'select(type=="object" and .rung=="R3" and .bench != null) | [.height, .bench.blocks, .seconds, .blocks_per_sec, (.bench.gas_per_sec/1e6), .bench.txs, .bench.max_rss_mb, .bench.gomaxprocs, .bench.host] | @tsv' "$f" 2>/dev/null || true)
      [ -n "$line" ] && best+="$line"$'\t'"$f"$'\n'
    done
  done
  if [ -n "$best" ]; then
    while IFS=$'\t' read -r h blocks secs bps mgas txs rss procs host file; do
      kv "to $h" "$(printf '%d blocks in %.0f s: %.0f blocks/s, %.0f Mgas/s, %d txs, %.0f MB RSS, %s cores (%s)' "$blocks" "$secs" "$bps" "$mgas" "$txs" "$rss" "$procs" "$(basename "$file")")"
    done < <(printf '%s' "$best" | sort -t$'\t' -k2,2nr | head -6)
    J[replay_best]=$(printf '%s' "$best" | sort -t$'\t' -k2,2nr | head -1 | awk -F'\t' '{printf "{\"height\":%s,\"blocks\":%s,\"seconds\":%s,\"blocks_per_sec\":%s,\"mgas_per_sec\":%s}", $1, $2, $3, $4, $5}')
  else
    kv "none found" "in $results_dirs"
  fi
fi

# ---- live measurement --------------------------------------------------------
if [ "$run" = 1 ]; then
  out ""
  out "Measured now"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  bin="$tmp/goempo-dev"
  go build -o "$bin" ./cmd/goempo-dev
  if [ -s "$cache/blocks/blocks.jsonl" ]; then
    r1=$("$bin" score 2>/dev/null | tail -1)
    kv "R1 decode" "$(jq -r '"\(.score) (\(.pass)/\(.blocks) blocks, \(.txs) txs)"' <<< "$r1" 2>/dev/null || echo "$r1")"
    J[r1]=$(jq -c '{score, blocks, pass, txs}' <<< "$r1" 2>/dev/null || echo null)
  fi
  r2=$("$bin" genesis 2>&1 | tail -1)
  kv "R2 genesis" "$r2"
  J[r2]="\"$r2\""
  if [ -d "$cache/rpc" ]; then
    r5=$("$bin" score -rung R5 2>/dev/null | tail -1)
    kv "R5 RPC" "$(jq -r '"\(.methods_identical)/\(.methods) methods, \(.cases_identical)/\(.cases) cases"' <<< "$r5" 2>/dev/null || echo "$r5")"
    J[r5]=$(jq -c '{methods, methods_identical, cases, cases_identical}' <<< "$r5" 2>/dev/null || echo null)
  fi
  if [ -d "$cache/mainnet" ]; then
    r3=$("$bin" replay -to "$replay_to" -diagnose off 2>/dev/null | tail -1)
    kv "R3 replay 1..$replay_to" "$(jq -r '"height \(.height): \(.blocks_per_sec|floor) blocks/s, \(.bench.gas_per_sec/1e6|floor) Mgas/s, \(.bench.txs) txs, \(.seconds*10|floor/10) s on \(.bench.gomaxprocs) cores"' <<< "$r3" 2>/dev/null || echo "$r3")"
    J[r3_window]=$(jq -c '{height, seconds, blocks_per_sec}' <<< "$r3" 2>/dev/null || echo null)
  fi
  if [ "$sim" = 1 ]; then
    start=$(date +%s)
    if go test -count=1 ./internal/sim/ >/dev/null 2>&1; then simres="pass"; else simres="FAIL"; fi
    kv "R6 local sim tests" "$simres ($(( $(date +%s) - start )) s, go test ./internal/sim/)"
    J[r6_sim]="\"$simres\""
  fi
fi

if [ "$json" = 1 ]; then
  first_key=1; printf '{'
  for k in "${!J[@]}"; do
    [ $first_key = 1 ] || printf ','
    first_key=0; printf '"%s":%s' "$k" "${J[$k]}"
  done
  printf '}\n'
fi
