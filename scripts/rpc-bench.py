#!/usr/bin/env python3
"""RPC latency benchmark for goempo-dev serve (and the reference node).

Sends sequential JSON-RPC requests over one keep-alive HTTP connection per
method and prints p50/p90/p99/mean latency in milliseconds as JSON lines.

  scripts/rpc-bench.py -url http://127.0.0.1:8545 -n 300 -blocks 3211:5437
"""
import argparse
import http.client
import json
import random
import statistics
import time
import urllib.parse

PATHUSD = "0x20c0000000000000000000000000000000000000"
FEE_MANAGER = "0xfeec000000000000000000000000000000000000"


def methods(state, lo, hi, busy):
    def pick():
        return hex(random.choice(busy)) if busy else hex(random.randint(lo, hi))

    holder = "0x" + "00" * 19 + "01"
    balance_of = "0x70a08231" + "00" * 12 + holder[2:]
    return {
        "eth_blockNumber": lambda: [],
        "eth_getBlockByNumber_full": lambda: [pick(), True],
        "eth_getBlockReceipts": lambda: [pick()],
        "eth_call_balanceOf": lambda: [{"to": PATHUSD, "data": balance_of}, state],
        "eth_getBalance": lambda: [FEE_MANAGER, state],
        "eth_getProof": lambda: [PATHUSD, ["0x" + "00" * 31 + "%02x" % random.randint(0, 15)], state],
        "debug_traceBlockByNumber_callTracer": lambda: [pick(), {"tracer": "callTracer"}],
    }


def call(conn, path, method, params, rid):
    body = json.dumps({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
    t0 = time.perf_counter()
    conn.request("POST", path, body, {"content-type": "application/json"})
    resp = conn.getresponse()
    data = resp.read()
    dt = time.perf_counter() - t0
    ok = resp.status == 200 and "error" not in json.loads(data)
    return dt, ok, len(data)


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(round(p / 100 * (len(xs) - 1))))]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-url", default="http://127.0.0.1:8545")
    ap.add_argument("-n", type=int, default=300)
    ap.add_argument("-blocks", default="3211:5437", help="lo:hi block range for block methods")
    ap.add_argument("-busy", default="", help="file of block numbers (one per line) to sample instead of the range")
    ap.add_argument("-only", default="", help="comma-separated method names")
    ap.add_argument("-seed", type=int, default=1)
    ap.add_argument("-state", default="latest", help="block tag for state methods (eth_call, eth_getBalance, eth_getProof)")
    ap.add_argument("-label", default="")
    a = ap.parse_args()
    random.seed(a.seed)
    u = urllib.parse.urlparse(a.url)
    lo, hi = (int(x) for x in a.blocks.split(":"))
    busy = [int(x) for x in open(a.busy).read().split()] if a.busy else []
    conn = http.client.HTTPConnection(u.hostname, u.port or 80, timeout=120)
    path = u.path or "/"
    _, _, _ = call(conn, path, "eth_blockNumber", [], 0)
    only = set(a.only.split(",")) if a.only else None
    for name, params in methods(a.state, lo, hi, busy).items():
        if only and name not in only:
            continue
        method = name.split("_full")[0].split("_callTracer")[0].split("_balanceOf")[0]
        lat, errors, size = [], 0, 0
        for i in range(a.n):
            dt, ok, n = call(conn, path, method, params(), i + 1)
            lat.append(dt * 1000)
            errors += 0 if ok else 1
            size += n
        print(json.dumps({
            "label": a.label, "method": name, "n": a.n, "errors": errors,
            "p50_ms": round(pct(lat, 50), 3), "p90_ms": round(pct(lat, 90), 3),
            "p99_ms": round(pct(lat, 99), 3), "mean_ms": round(statistics.mean(lat), 3),
            "avg_bytes": size // a.n,
        }), flush=True)


if __name__ == "__main__":
    main()
