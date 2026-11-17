#!/usr/bin/env python3
"""Medians of rpc-load.py run output across rounds, goempo vs Rust side by side.

  rpc-summary.py run-goempo-0.json run-rust-0.json ...

Each file is one rpc-load.py run (-out); its label is "<node>-<round>". Prints, per method and concurrency, each node's median
requests/s and p50/p99 latency across rounds and the goempo/Rust throughput
ratio.
"""

import json
import statistics
import sys


def main():
    rows = []
    for p in sys.argv[1:]:
        with open(p) as f:
            rows += json.load(f)["rows"]
    cell = {}
    for r in rows:
        node = r["label"].rsplit("-", 1)[0]
        cell.setdefault((r["method"], r["c"], node), []).append(r)
    keys = sorted({(m, c) for m, c, _ in cell})
    print("| method | conc | goempo req/s | Rust req/s | goempo/Rust | goempo p50/p99 ms | Rust p50/p99 ms | errors |")
    print("| --- | --- | --- | --- | --- | --- | --- | --- |")
    for m, c in keys:
        g, r = cell.get((m, c, "goempo"), []), cell.get((m, c, "rust"), [])
        if not g or not r:
            continue
        med = lambda rs, k: statistics.median(x[k] for x in rs)
        gr, rr = med(g, "rps"), med(r, "rps")
        errs = sum(x["errors"] for x in g + r)
        print(f"| {m} | {c} | {gr:,.0f} | {rr:,.0f} | {gr / rr:.2f} | {med(g, 'p50_ms'):.3f}/{med(g, 'p99_ms'):.3f} "
              f"| {med(r, 'p50_ms'):.3f}/{med(r, 'p99_ms'):.3f} | {errs} |")


if __name__ == "__main__":
    main()
