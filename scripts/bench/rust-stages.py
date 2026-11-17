#!/usr/bin/env python3
"""Per-stage timings of a `tempo import` run, read from its log.

`tempo import` runs reth's staged pipeline once over the whole file:
Headers, Bodies, SenderRecovery, Execution, Merkle (one state root for the
whole range, checked against the last header), then the lookup and history
index stages. This script prints how long each stage took and whether the
import reached the file's last block.

Tempo 1.18.x exits 1 after a complete import when the file's transaction
count differs from the count of transaction numbers it assigned:
"Imported 41084/41084 blocks, 88187/19243 transactions". The run is a
success when every block was imported and the Finish stage reached the
target, so this script, not the exit code, decides.

  rust-stages.py LOG [-json OUT]
"""

import argparse
import datetime
import json
import re
import sys

ANSI = re.compile(r"\x1b\[[0-9;]*m")
STAGE = re.compile(r"^(\S+)\s+INFO (Executing|Finished) stage .*stage=(\w+) checkpoint=(\d+) target=(\d+)")
DONE = re.compile(r"Imported (\d+)/(\d+) blocks")


def ts(s):
    return datetime.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()


def parse(path):
    open_at, total, finish_cp, target = {}, {}, None, None
    imported = decoded = None
    t0 = t1 = None
    with open(path, errors="replace") as f:
        for raw in f:
            line = ANSI.sub("", raw)
            m = STAGE.match(line)
            if m:
                t, kind, stage = ts(m.group(1)), m.group(2), m.group(3)
                t0 = t if t0 is None else t0
                t1 = t
                if kind == "Executing":
                    open_at.setdefault(stage, t)
                elif stage in open_at:
                    total[stage] = total.get(stage, 0.0) + t - open_at.pop(stage)
                    if stage == "Finish":
                        finish_cp, target = int(m.group(4)), int(m.group(5))
                continue
            m = DONE.search(line)
            if m:
                imported, decoded = int(m.group(1)), int(m.group(2))
    stages = {s: round(v, 3) for s, v in total.items()}
    ok = finish_cp is not None and finish_cp == target and (imported is None or imported == decoded)
    return {
        "stages_s": stages,
        "pipeline_s": round(t1 - t0, 3) if t0 is not None else None,
        "finish_checkpoint": finish_cp,
        "target": target,
        "blocks_imported": imported,
        "blocks_decoded": decoded,
        "ok": ok,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("log")
    ap.add_argument("-json", default="")
    a = ap.parse_args()
    r = parse(a.log)
    out = json.dumps(r, indent=1)
    if a.json:
        open(a.json, "w").write(out + "\n")
    print(out)
    sys.exit(0 if r["ok"] else 1)


if __name__ == "__main__":
    main()
