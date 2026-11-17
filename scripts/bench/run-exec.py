#!/usr/bin/env python3
"""Interleaved head-to-head block execution runs: Rust tempo vs goempo.

Each round copies a fresh starting datadir for each node, then runs, pinned
to the same cores through measure.py:

  rust:   tempo import --datadir <copy> --chain mainnet <range.rlp>
  goempo: goempo-dev replay -datadir <copy> -dir <chunks> -to <last> -diagnose off

over the same blocks, alternating which node goes first, and appends one JSON
line per run to -out. Rust's line carries its per-stage timings
(rust-stages.py); goempo's carries replay's own result (blocks, gas, txs).
An empty -rust-base or -go-base starts that node from genesis.

  run-exec.py -label recent -rounds 5 -cores 4-7,20-23 \\
    -tempo ./rel/tempo -rlp recent.rlp -rust-base snap-49408911 \\
    -goempo ./bin/goempo-dev -chunks chunks -go-base gdd-warm -to 49449999 \\
    -out logs/exec-recent.jsonl
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)


def stages(log):
    out = subprocess.run([sys.executable, os.path.join(HERE, "rust-stages.py"), log], capture_output=True, text=True)
    try:
        return json.loads(out.stdout)
    except json.JSONDecodeError:
        return {"ok": False, "error": out.stderr[-500:]}


def fresh(base, dst):
    shutil.rmtree(dst, ignore_errors=True)
    if base:
        subprocess.run(["cp", "-a", base, dst], check=True)
    else:
        os.makedirs(dst)
    subprocess.run(["sync"], check=True)


def measure(a, label, log, js, cmd):
    if os.path.exists(log):
        os.remove(log)
    subprocess.run([sys.executable, os.path.join(HERE, "measure.py"), "-out", js, "-cores", a.cores,
                    "-label", label, "-log", log, "--"] + cmd)
    with open(js) as f:
        return json.load(f)


def run_rust(a, r):
    dd = os.path.join(a.work, "rust-dd")
    fresh(a.rust_base, dd)
    log = os.path.join(a.work, f"{a.label}-rust-{r}.log")
    m = measure(a, f"rust-{a.label}-{r}", log, log + ".json",
                [a.tempo, "import", "--datadir", dd, "--chain", "mainnet", a.rlp])
    m["node"] = "rust"
    m["stages"] = stages(log)
    m["ok"] = m["stages"].get("ok", False)
    shutil.rmtree(dd, ignore_errors=True)
    return m


def run_go(a, r):
    dd = os.path.join(a.work, "go-dd")
    fresh(a.go_base, dd)
    log = os.path.join(a.work, f"{a.label}-go-{r}.log")
    res = os.path.join(a.work, f"{a.label}-go-{r}.result.json")
    if os.path.exists(res):
        os.remove(res)
    m = measure(a, f"goempo-{a.label}-{r}", log, log + ".json",
                [a.goempo, "replay", "-datadir", dd, "-dir", a.chunks, "-to", str(a.to),
                 "-diagnose", "off", "-result", res])
    m["node"] = "goempo"
    try:
        with open(res) as f:
            m["replay"] = json.load(f)
    except (OSError, json.JSONDecodeError):
        m["replay"] = None
    m["ok"] = m["exit"] == 0 and m["replay"] is not None and m["replay"].get("height") == a.to
    shutil.rmtree(dd, ignore_errors=True)
    return m


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-label", required=True)
    ap.add_argument("-rounds", type=int, default=5)
    ap.add_argument("-cores", required=True)
    ap.add_argument("-tempo", required=True)
    ap.add_argument("-rlp", required=True)
    ap.add_argument("-rust-base", dest="rust_base", default="")
    ap.add_argument("-goempo", required=True)
    ap.add_argument("-chunks", required=True)
    ap.add_argument("-go-base", dest="go_base", default="")
    ap.add_argument("-to", type=int, required=True)
    ap.add_argument("-work", default="exec-work")
    ap.add_argument("-out", required=True)
    ap.add_argument("-only", default="", help="rust or goempo: run one node only")
    a = ap.parse_args()
    os.makedirs(a.work, exist_ok=True)
    runners = [("rust", run_rust), ("goempo", run_go)]
    if a.only:
        runners = [x for x in runners if x[0] == a.only]
    for r in range(a.rounds):
        order = runners if r % 2 == 0 else runners[::-1]
        for name, fn in order:
            m = fn(a, r)
            m["round"] = r
            m["range_label"] = a.label
            with open(a.out, "a") as f:
                f.write(json.dumps(m) + "\n")
            print(f"{time.strftime('%H:%M:%S')} {a.label} round {r} {name} ok={m['ok']} wall={m['wall_s']}s "
                  f"cpu={m['cpu_s']}s rss={m['max_rss_mb']}MB load={m['host_load']}", flush=True)


if __name__ == "__main__":
    main()
