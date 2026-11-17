#!/usr/bin/env bash
# Reproducible Tempo devnet: N validators from a fresh genesis and DKG, Rust
# tempo nodes by default, any slot replaceable by goempo. See README.md here.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WORK=${DEVNET_WORK:-$HOME/goempo-work/devnet}
TEMPO_REV=${TEMPO_REV:-c8b89f9b6d}
TEMPO_SRC=${TEMPO_SRC:-$WORK/tempo}
TEMPO=${TEMPO_BIN:-$WORK/target/release/tempo}
XTASK=${XTASK_BIN:-$WORK/target/release/tempo-xtask}
GOEMPO=${GOEMPO_BIN:-$WORK/bin/goempo}
NET=${DEVNET_NET:-$WORK/net}
N=${DEVNET_VALIDATORS:-4}
BASE_PORT=${DEVNET_BASE_PORT:-21000}
EPOCH=${DEVNET_EPOCH_LENGTH:-60}
SEED=${DEVNET_SEED:-42}
HARDFORK=${DEVNET_HARDFORK-T13}
GOEMPO_SLOTS=${DEVNET_GOEMPO:-}
LOCALNET_SECRET=tempo-localnet-signing-key-secret

die() { echo "devnet: $*" >&2; exit 1; }

port() { echo $((BASE_PORT + 100 * $1 + $2)); }

slot_dir() { echo "$NET/127.0.0.1:$(port "$1" 0)"; }

is_goempo() {
	local s
	for s in ${GOEMPO_SLOTS//,/ }; do [ "$s" = "$1" ] && return 0; done
	return 1
}

rpc() {
	curl -s -m 3 -H 'content-type: application/json' \
		--data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$2\",\"params\":${3:-[]}}" \
		"http://127.0.0.1:$(port "$1" 5)"
}

cmd_build() {
	if [ ! -d "$TEMPO_SRC/.git" ]; then
		git clone -q https://github.com/tempoxyz/tempo.git "$TEMPO_SRC"
	fi
	git -C "$TEMPO_SRC" fetch -q origin
	git -C "$TEMPO_SRC" checkout -q "$TEMPO_REV"
	(cd "$TEMPO_SRC" && CARGO_TARGET_DIR="$WORK/target" cargo build --release \
		-j "${DEVNET_JOBS:-16}" --bin tempo --bin tempo-xtask)
	"$TEMPO" --version | head -3
}

cmd_gen() {
	[ -x "$XTASK" ] || die "no tempo-xtask at $XTASK (run: $0 build)"
	cmd_stop >/dev/null 2>&1 || true
	local addrs="" i
	for ((i = 0; i < N; i++)); do
		addrs+="${addrs:+,}127.0.0.1:$(port $i 0)"
	done
	mkdir -p "$NET"
	"$XTASK" generate-localnet -o "$NET" --force --validators "$addrs" \
		--seed "$SEED" --epoch-length "$EPOCH" ${HARDFORK:+--hardfork "$HARDFORK"} --accounts "${DEVNET_ACCOUNTS:-10}" \
		>"$WORK/gen.log" 2>&1 || { tail -20 "$WORK/gen.log"; die "generate-localnet failed"; }
	for ((i = 0; i < N; i++)); do
		local d
		d=$(slot_dir $i)
		python3 "$HERE/agekey.py" "$d/signing.key" "$LOCALNET_SECRET" >"$d/signing.key.hex"
		chmod 600 "$d/signing.key.hex"
	done
	python3 "$HERE/nodes.py" "$NET" "$WORK/gen.log" "$N" "$BASE_PORT" "$EPOCH" >"$NET/nodes.json"
	echo "devnet: generated $N validators, epoch length $EPOCH, seed $SEED, latest fork ${HARDFORK:-all} in $NET"
}

trusted_peers() {
	local out="" i
	for ((i = 0; i < N; i++)); do
		is_goempo $i && continue
		out+="${out:+,}enode://$(cat "$(slot_dir $i)/enode.identity")@127.0.0.1:$(port $i 1)"
	done
	echo "$out"
}

start_rust() {
	local i=$1 d
	d=$(slot_dir $i)
	mkdir -p "$d/logs"
	nohup "$TEMPO" node \
		--chain "$NET/genesis.json" \
		--datadir "$d/data" \
		--http --http.addr 127.0.0.1 --http.port "$(port $i 5)" --http.api all \
		--ws --ws.addr 127.0.0.1 --ws.port "$(port $i 5)" --ws.api all \
		--metrics "127.0.0.1:$(port $i 6)" \
		--ipcdisable \
		--log.file.directory "$d/logs" \
		--tempo.bootnodes-endpoint none \
		--consensus.signing-key "$d/signing.key.hex" \
		--consensus.signing-share "$d/signing.share" \
		--consensus.listen-address "127.0.0.1:$(port $i 0)" \
		--consensus.metrics-address "127.0.0.1:$(port $i 2)" \
		--trusted-peers "$(trusted_peers)" \
		--port "$(port $i 1)" --discovery.port "$(port $i 1)" \
		--discovery.v5.port "$(port $i 4)" \
		--p2p-secret-key "$d/enode.key" \
		--authrpc.port "$(port $i 3)" \
		--consensus.use-local-defaults --consensus.bypass-ip-check \
		${DEVNET_RUST_ARGS:-} >"$d/node.log" 2>&1 &
	echo $! >"$d/pid"
	echo "devnet: slot $i rust pid $! rpc http://127.0.0.1:$(port $i 5)"
}

start_goempo() {
	local i=$1 d peers="" j
	d=$(slot_dir $i)
	[ -x "$GOEMPO" ] || die "no goempo at $GOEMPO"
	for ((j = 0; j < N; j++)); do
		[ $j = $i ] && continue
		peers+="${peers:+,}$(cat "$(slot_dir $j)/pubkey")@127.0.0.1:$(port $j 0)"
	done
	local args=${DEVNET_GOEMPO_ARGS:-"validator --chain {genesis} --datadir {datadir} --consensus.signing-key {key} --consensus.signing-share {share} --consensus.listen-address {listen} --consensus.metrics-address {metrics} --consensus.bypass-ip-check --http {http}"}
	args=${args//\{genesis\}/$NET/genesis.json}
	args=${args//\{datadir\}/$d/goempo}
	args=${args//\{key\}/$d/signing.key.hex}
	args=${args//\{share\}/$d/signing.share}
	args=${args//\{listen\}/127.0.0.1:$(port $i 0)}
	args=${args//\{peers\}/$peers}
	args=${args//\{http\}/127.0.0.1:$(port $i 5)}
	args=${args//\{metrics\}/127.0.0.1:$(port $i 2)}
	# shellcheck disable=SC2086
	nohup "$GOEMPO" $args >"$d/node.log" 2>&1 &
	echo $! >"$d/pid"
	echo "devnet: slot $i goempo pid $! rpc http://127.0.0.1:$(port $i 5)"
}

slot_running() {
	local d
	d=$(slot_dir "$1")
	[ -f "$d/pid" ] && kill -0 "$(cat "$d/pid")" 2>/dev/null
}

port_busy() {
	ss -Htln "sport = :$1" 2>/dev/null | grep -q .
}

start_slot() {
	if slot_running "$1"; then
		echo "devnet: slot $1 already running (pid $(cat "$(slot_dir "$1")/pid"))"
		return 0
	fi
	if port_busy "$(port "$1" 0)" || port_busy "$(port "$1" 5)"; then
		die "slot $1: port $(port "$1" 0) or $(port "$1" 5) is in use by a process this harness does not track"
	fi
	if is_goempo "$1"; then start_goempo "$1"; else start_rust "$1"; fi
	sleep 1
	kill -0 "$(cat "$(slot_dir "$1")/pid")" 2>/dev/null || { tail -5 "$(slot_dir "$1")/node.log"; die "slot $1 exited at start"; }
}

stop_slot() {
	local d pid
	d=$(slot_dir "$1")
	[ -f "$d/pid" ] || return 0
	pid=$(cat "$d/pid")
	if kill -0 "$pid" 2>/dev/null; then
		kill "$pid"
		local k
		for ((k = 0; k < 30; k++)); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
		kill -9 "$pid" 2>/dev/null || true
	fi
	rm -f "$d/pid"
	echo "devnet: slot $1 stopped"
}

cmd_start() {
	[ -f "$NET/nodes.json" ] || die "no devnet at $NET (run: $0 gen)"
	local i
	if [ $# -gt 0 ]; then
		for i in "$@"; do start_slot "$i"; done
		return
	fi
	for ((i = 0; i < N; i++)); do start_slot $i; done
}

cmd_stop() {
	local i
	if [ $# -gt 0 ]; then
		for i in "$@"; do stop_slot "$i"; done
		return
	fi
	for ((i = 0; i < N; i++)); do stop_slot $i; done
}

cmd_wipe() {
	local i
	if [ $# -gt 0 ]; then
		for i in "$@"; do stop_slot "$i"; rm -rf "$(slot_dir "$i")/data" "$(slot_dir "$i")/goempo"; done
		return
	fi
	for ((i = 0; i < N; i++)); do
		stop_slot $i
		rm -rf "$(slot_dir $i)/data" "$(slot_dir $i)/goempo"
	done
}

cmd_status() {
	local i
	for ((i = 0; i < N; i++)); do
		local d kind=rust alive=down head fin
		d=$(slot_dir $i)
		is_goempo $i && kind=goempo
		[ -f "$d/pid" ] && kill -0 "$(cat "$d/pid")" 2>/dev/null && alive=up
		head=$(rpc $i eth_blockNumber | python3 -c 'import json,sys; print(int(json.load(sys.stdin)["result"],16))' 2>/dev/null || echo -)
		fin=$(rpc $i eth_getBlockByNumber '["finalized",false]' | python3 -c 'import json,sys; print(int(json.load(sys.stdin)["result"]["number"],16))' 2>/dev/null || echo -)
		echo "slot $i $kind $alive head=$head finalized=$fin"
	done
}

cmd_monitor() {
	python3 "$HERE/monitor.py" --nodes "$NET/nodes.json" "$@"
}

cmd_votes() {
	python3 "$HERE/votes.py" --nodes "$NET/nodes.json" "$@"
}

cmd_quorum() {
	[ $# -ge 2 ] || die "usage: $0 quorum <slot-to-stop> <seconds> [monitor args]"
	local slot=$1 secs=$2
	shift 2
	stop_slot "$slot"
	python3 "$HERE/monitor.py" --nodes "$NET/nodes.json" --duration "$secs" --skip "$slot" "$@" || true
	start_slot "$slot"
}

cmd_r6e() {
	[ -n "$GOEMPO_SLOTS" ] || die "r6e needs DEVNET_GOEMPO=<goempo slots>"
	local secs=${1:-3600} out rust="" i
	out="$WORK/reports/r6e-$(date -u +%Y%m%dT%H%M%S)"
	mkdir -p "$out"
	for ((i = 0; i < N; i++)); do is_goempo $i || rust+="${rust:+ }$i"; done
	set -- $rust
	echo "devnet: r6e goempo slots $GOEMPO_SLOTS, rust slots $rust, reports in $out"
	python3 "$HERE/monitor.py" --nodes "$NET/nodes.json" --duration 120 --stall 30 --report "$out/warmup.json" >"$out/warmup.log" 2>&1 || true
	stop_slot "$1"
	python3 "$HERE/monitor.py" --nodes "$NET/nodes.json" --duration 120 --stall 20 --skip "$1" --report "$out/quorum.json" >"$out/quorum.log" 2>&1 || true
	start_slot "$1"
	python3 "$HERE/monitor.py" --nodes "$NET/nodes.json" --duration "$secs" --stall 30 --report "$out/long.json" >"$out/long.log" 2>&1 || true
	python3 "$HERE/votes.py" --nodes "$NET/nodes.json" --json "$out/votes.json" >"$out/votes.log" 2>&1 || true
	python3 "$HERE/dealers.py" --nodes "$NET/nodes.json" --slot "$1" --json "$out/dealers.json" >"$out/dealers.log" 2>&1 || true
	python3 - "$out" "$GOEMPO_SLOTS" <<'EOF'
import json, sys
out, g = sys.argv[1], [x for x in sys.argv[2].split(",") if x]
q = json.load(open(out + "/quorum.json"))
l = json.load(open(out + "/long.json"))
dl = json.load(open(out + "/dealers.json"))
done = [e for k, e in dl.items() if e["outcome_bytes"]]
res = {
    "votes_counted": q["end_finalized"] is not None and q["start_finalized"] is not None and q["end_finalized"] > q["start_finalized"] and not q["stalls"],
    "quorum_window": [q["start_finalized"], q["end_finalized"]],
    "goempo_proposed_finalized": sum(l["proposers"].get(s, 0) for s in g),
    "proposers": l["proposers"],
    "ceremonies": l["ceremonies"],
    "boundaries": len(l["boundaries"]),
    "forks": len(l["forks"]),
    "stalls": len(l["stalls"]),
    "lagging": l["lagging"],
    "finalized": [l["start_finalized"], l["end_finalized"]],
    "duration_s": l["duration_s"],
    "epochs_with_outcome": len(done),
    "epochs_goempo_dealt": sum(1 for e in done if all(x in e["dealer_logs"] for x in g)),
}
res["pass"] = (res["votes_counted"] and res["goempo_proposed_finalized"] > 0 and res["forks"] == 0
               and res["stalls"] == 0 and not res["lagging"] and res["ceremonies"] == res["boundaries"] > 0
               and res["epochs_goempo_dealt"] == res["epochs_with_outcome"] > 0)
json.dump(res, open(out + "/summary.json", "w"), indent=1)
print(json.dumps(res))
EOF
}

usage() {
	cat <<EOF
usage: $0 <build|gen|start|stop|wipe|status|monitor|quorum|votes|dealers|r6e> [args]
  build                 clone tempoxyz/tempo at TEMPO_REV and build tempo, tempo-xtask
  gen                   fresh genesis + DKG for N validators into NET (stops the net)
  start [slot...]       start all (or the given) slots; DEVNET_GOEMPO=0,1 runs those slots as goempo
  stop [slot...]        stop all (or the given) slots
  wipe [slot...]        stop and delete chain data, keeping keys and genesis
  status                head and finalized height per slot
  monitor [--duration S] [--report FILE]   forks, stalls, proposers, DKG epochs
  quorum <slot> <S>     stop one slot for S seconds and check finality continues
  r6e [S]               with DEVNET_GOEMPO set: quorum test, S-second soak (3600), votes; summary.json
  dealers               per epoch: slots whose DKG dealer logs reached the chain
  votes [--slots 1,2]   per Rust slot: votes received from each peer, latest vote view, WARNs naming it
env: DEVNET_WORK=$WORK N=$N BASE_PORT=$BASE_PORT EPOCH=$EPOCH SEED=$SEED DEVNET_HARDFORK=$HARDFORK (set empty for every fork)
EOF
}

main() {
	local cmd=${1:-}
	[ $# -gt 0 ] && shift
	case "$cmd" in
	build) cmd_build ;;
	gen) cmd_gen ;;
	start) cmd_start "$@" ;;
	stop) cmd_stop "$@" ;;
	wipe) cmd_wipe "$@" ;;
	status) cmd_status ;;
	monitor) cmd_monitor "$@" ;;
	quorum) cmd_quorum "$@" ;;
	votes) cmd_votes "$@" ;;
	r6e) cmd_r6e "$@" ;;
	dealers) python3 "$HERE/dealers.py" --nodes "$NET/nodes.json" "$@" ;;
	*) usage; exit 2 ;;
	esac
}

main "$@"
