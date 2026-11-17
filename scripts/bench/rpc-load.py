#!/usr/bin/env python3
"""JSON-RPC latency under concurrency, same requests against each node.

  corpus: build a fixed request list from blocks [from, to] served by -url
  run:    send each method's requests from -c workers (one process and one
          keep-alive HTTP connection per worker, so the client's GIL is not
          the bottleneck) and report p50/p90/p99/max latency and requests/s
  diff:   send every corpus request to -a and -b and count differing results

  scripts/bench/rpc-load.py corpus -url http://127.0.0.1:8545 -from 49459000 -to 49459999 -out corpus.json
  scripts/bench/rpc-load.py run -url http://127.0.0.1:8545 -corpus corpus.json -c 1,4,16,64 -label goempo -out goempo.json
  scripts/bench/rpc-load.py diff -a http://127.0.0.1:8545 -b http://127.0.0.1:8546 -corpus corpus.json
"""
import argparse
import hashlib
import http.client
import json
import multiprocessing as mp
import os
import random
import time
import urllib.parse

PATHUSD = "0x20c0000000000000000000000000000000000000"
METHODS = ["eth_getBalance", "eth_call", "eth_getBlockByNumber", "eth_getLogs", "debug_traceTransaction"]


class Client:
    def __init__(self, url):
        u = urllib.parse.urlparse(url)
        self.host, self.port, self.path = u.hostname, u.port or 80, u.path or "/"
        self.conn = http.client.HTTPConnection(self.host, self.port, timeout=120)
        self.rid = 0

    def call(self, method, params):
        self.rid += 1
        body = json.dumps({"jsonrpc": "2.0", "id": self.rid, "method": method, "params": params})
        for attempt in range(2):
            try:
                self.conn.request("POST", self.path, body, {"content-type": "application/json"})
                resp = self.conn.getresponse()
                return resp.status, resp.read()
            except (http.client.HTTPException, OSError):
                self.conn.close()
                self.conn = http.client.HTTPConnection(self.host, self.port, timeout=120)
                if attempt == 1:
                    raise
        raise RuntimeError("unreachable")

    def result(self, method, params):
        status, data = self.call(method, params)
        msg = json.loads(data)
        if status != 200 or "error" in msg:
            raise RuntimeError("%s %s: %s" % (method, params, data[:300]))
        return msg["result"]


def build_corpus(a):
    c = Client(a.url)
    rng = random.Random(a.seed)
    head = hex(a.hi)
    blocks = {}
    for n in range(a.lo, a.hi + 1):
        blocks[n] = c.result("eth_getBlockByNumber", [hex(n), True])
    txs = [(n, t) for n, b in blocks.items() for t in b["transactions"]]
    senders = sorted({t["from"] for _, t in txs})
    tokens = sorted({t["to"] for _, t in txs if t.get("to") and t["to"].startswith("0x20c0")}) or [PATHUSD]
    reqs = {m: [] for m in METHODS}
    for _ in range(a.n):
        who = rng.choice(senders)
        reqs["eth_getBalance"].append([who, head])
        call = {"to": rng.choice(tokens), "data": "0x70a08231" + "00" * 12 + who[2:]}
        reqs["eth_call"].append([call, head])
        reqs["eth_getBlockByNumber"].append([hex(rng.randint(a.lo, a.hi)), True])
        lo = rng.randint(a.lo, a.hi - 9)
        reqs["eth_getLogs"].append([{"fromBlock": hex(lo), "toBlock": hex(lo + 9), "address": rng.choice(tokens)}])
        reqs["debug_traceTransaction"].append([rng.choice(txs)[1]["hash"], {"tracer": "callTracer"}])
    out = {"from": a.lo, "to": a.hi, "seed": a.seed, "blocks": len(blocks), "txs": len(txs), "requests": reqs}
    with open(a.out, "w") as f:
        json.dump(out, f)
    print("corpus: %d blocks, %d txs, %d senders, %d tokens, %d requests per method" % (len(blocks), len(txs), len(senders), len(tokens), a.n))


def worker(url, method, params, barrier, out):
    c = Client(url)
    c.call(method, params[0])
    barrier.wait()
    lat, errs = [], 0
    t0 = time.perf_counter()
    for p in params:
        s = time.perf_counter()
        status, data = c.call(method, p)
        lat.append(time.perf_counter() - s)
        if status != 200 or b'"error"' in data[:200]:
            errs += 1
    out.put((t0, time.perf_counter(), lat, errs))


def pct(xs, p):
    return xs[min(len(xs) - 1, int(round(p / 100 * (len(xs) - 1))))]


def run_one(url, method, reqs, conc, per):
    barrier = mp.Barrier(conc)
    out = mp.Queue()
    procs = []
    for w in range(conc):
        params = [reqs[(w * per + i) % len(reqs)] for i in range(per)]
        p = mp.Process(target=worker, args=(url, method, params, barrier, out))
        p.start()
        procs.append(p)
    got = [out.get() for _ in procs]
    for p in procs:
        p.join()
    lat = sorted(x for g in got for x in g[2])
    span = max(g[1] for g in got) - min(g[0] for g in got)
    ms = lambda x: round(x * 1000, 3)
    return {
        "method": method, "c": conc, "n": len(lat), "errors": sum(g[3] for g in got),
        "rps": round(len(lat) / span, 1), "p50_ms": ms(pct(lat, 50)), "p90_ms": ms(pct(lat, 90)),
        "p99_ms": ms(pct(lat, 99)), "max_ms": ms(lat[-1]), "mean_ms": ms(sum(lat) / len(lat)),
    }


def run(a):
    with open(a.corpus) as f:
        corpus = json.load(f)
    rows = []
    for method in a.methods.split(","):
        reqs = corpus["requests"][method]
        for conc in [int(x) for x in a.c.split(",")]:
            per = max(a.per, -(-a.min_total // conc))
            row = run_one(a.url, method, reqs, conc, per)
            row["label"] = a.label
            rows.append(row)
            print(json.dumps(row), flush=True)
    res = {"label": a.label, "url": a.url, "corpus": a.corpus, "from": corpus["from"], "to": corpus["to"], "client_pid_cores": sorted(os.sched_getaffinity(0)), "rows": rows}
    with open(a.out, "w") as f:
        json.dump(res, f, indent=1)


def digest(v):
    return hashlib.sha256(json.dumps(v, sort_keys=True).encode()).hexdigest()


def diff(a):
    with open(a.corpus) as f:
        corpus = json.load(f)
    ca, cb = Client(a.a), Client(a.b)
    report = {}
    for method in METHODS:
        same = differ = 0
        example = None
        for p in corpus["requests"][method][: a.limit]:
            ra, rb = json.loads(ca.call(method, p)[1]), json.loads(cb.call(method, p)[1])
            if digest(ra.get("result", ra.get("error"))) == digest(rb.get("result", rb.get("error"))):
                same += 1
            else:
                differ += 1
                example = example or {"params": p, "a": json.dumps(ra)[:400], "b": json.dumps(rb)[:400]}
        report[method] = {"same": same, "differ": differ, "example": example}
        print(method, same, differ, flush=True)
    if a.out:
        with open(a.out, "w") as f:
            json.dump(report, f, indent=1)


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("corpus")
    p.add_argument("-url", required=True)
    p.add_argument("-from", dest="lo", type=int, required=True)
    p.add_argument("-to", dest="hi", type=int, required=True)
    p.add_argument("-n", type=int, default=400)
    p.add_argument("-seed", type=int, default=7)
    p.add_argument("-out", required=True)
    p = sub.add_parser("run")
    p.add_argument("-url", required=True)
    p.add_argument("-corpus", required=True)
    p.add_argument("-c", default="1,4,16,64")
    p.add_argument("-per", type=int, default=20, help="requests per worker at least")
    p.add_argument("-min-total", dest="min_total", type=int, default=400, help="requests per method and level at least")
    p.add_argument("-methods", default=",".join(METHODS))
    p.add_argument("-label", default="")
    p.add_argument("-out", required=True)
    p = sub.add_parser("diff")
    p.add_argument("-a", required=True)
    p.add_argument("-b", required=True)
    p.add_argument("-corpus", required=True)
    p.add_argument("-limit", type=int, default=100)
    p.add_argument("-out", default="")
    a = ap.parse_args()
    {"corpus": build_corpus, "run": run, "diff": diff}[a.cmd](a)


if __name__ == "__main__":
    main()
