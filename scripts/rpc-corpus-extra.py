#!/usr/bin/env python3
# Extra R5 requests: traces and call-family methods at replayable blocks.
# Usage: scripts/rpc-corpus-extra.py <statediff dir> <from> <to> > requests.jsonl
import glob, json, os, random, sys
d, lo, hi = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
random.seed(7)
blocks = []
for f in sorted(glob.glob(os.path.join(d, "*.jsonl"))):
    for l in open(f):
        e = json.loads(l)
        if lo <= e["n"] <= hi and e["txs"]:
            blocks.append(e)
picked = random.sample(blocks, min(12, len(blocks)))
out = []
def req(block, method, params):
    out.append({"block": block, "live": False, "request": json.dumps({"jsonrpc": "2.0", "id": 900000 + len(out), "method": method, "params": params}, separators=(",", ":"))})
for e in sorted(picked, key=lambda e: e["n"]):
    n, q = e["n"], hex(e["n"])
    req(n - 1, "debug_traceBlockByNumber", [q, {"tracer": "callTracer"}])
    req(n - 1, "debug_traceBlockByNumber", [q, {"tracer": "prestateTracer"}])
    req(n - 1, "debug_traceBlockByNumber", [q, {"tracer": "prestateTracer", "tracerConfig": {"diffMode": True}}])
    req(n - 1, "debug_traceBlockByNumber", [q, {}])
    req(n - 1, "trace_block", [q])
    for t in e["txs"][:2]:
        if "hash" in t:
            req(n - 1, "debug_traceTransaction", [t["hash"], {"tracer": "callTracer"}])
    addrs = []
    for t in e["txs"]:
        for side in ("pre", "post"):
            addrs += [a for a in t[side] if a not in addrs]
    token = "0x20c0000000000000000000000000000000000000"
    if addrs:
        req(n, "eth_createAccessList", [{"from": addrs[0], "to": token, "data": "0x70a08231" + "0" * 24 + addrs[0][2:]}, q])
        req(n, "debug_traceCall", [{"from": addrs[0], "to": token, "data": "0x06fdde03"}, q, {"tracer": "callTracer"}])
        req(n, "eth_call", [{"from": addrs[0], "to": token, "data": "0xa9059cbb" + "0" * 24 + addrs[-1][2:] + "f" * 64}, q])
        req(n, "eth_estimateGas", [{"from": addrs[0], "to": token, "data": "0xa9059cbb" + "0" * 24 + addrs[-1][2:] + "0" * 63 + "1"}, q])
        req(n, "eth_simulateV1", [{"blockStateCalls": [{"calls": [{"from": addrs[0], "to": token, "data": "0x06fdde03"}]}]}, q])
for o in out:
    print(json.dumps(o, separators=(",", ":")))
