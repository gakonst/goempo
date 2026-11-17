#!/usr/bin/env python3
"""Medians and spread of run-exec.py JSON lines, per range and node.

  summarize.py exec-recent.jsonl [exec-busy.jsonl ...] [-md]
"""

import json
import statistics
import sys


def med(xs):
    return statistics.median(xs) if xs else float("nan")


def spread(xs):
    return (min(xs), max(xs)) if xs else (float("nan"), float("nan"))


def blocks_gas(r):
    if r["node"] == "goempo" and r.get("replay"):
        b = r["replay"]["bench"]
        return b["blocks"], b["gas"]
    return None, None


def main():
    paths = [p for p in sys.argv[1:] if not p.startswith("-")]
    rows = []
    for p in paths:
        with open(p) as f:
            rows += [json.loads(line) for line in f if line.strip()]
    groups = {}
    for r in rows:
        if r.get("ok"):
            groups.setdefault((r["range_label"], r["node"]), []).append(r)
    work = {}
    for (rng, node), rs in groups.items():
        if node == "goempo":
            work[rng] = blocks_gas(rs[0])
    print("| range | node | runs | wall s median (min-max) | exec s median | CPU s median | peak RSS MB | blocks/s | Mgas/s | host load mean |")
    print("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
    for (rng, node), rs in sorted(groups.items()):
        wall = [r["wall_s"] for r in rs]
        cpu = [r["cpu_s"] for r in rs]
        rss = [r["max_rss_mb"] for r in rs]
        load = [r["host_load"]["mean"] for r in rs if r.get("host_load")]
        if node == "rust":
            ex = [r["stages"]["stages_s"].get("Execution", 0) + r["stages"]["stages_s"].get("MerkleExecute", 0) for r in rs]
        else:
            ex = [r["replay"]["seconds"] for r in rs]
        blocks, gas = work.get(rng, (None, None))
        e = med(ex)
        bps = f"{blocks / e:,.0f}" if blocks and e else "?"
        mg = f"{gas / e / 1e6:,.1f}" if gas and e else "?"
        lo, hi = spread(wall)
        print(f"| {rng} | {node} | {len(rs)} | {med(wall):.2f} ({lo:.2f}-{hi:.2f}) | {e:.2f} | {med(cpu):.2f} | {med(rss):,.0f} | {bps} | {mg} | {med(load):.0f} |")


if __name__ == "__main__":
    main()
