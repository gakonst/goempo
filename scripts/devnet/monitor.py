#!/usr/bin/env python3
"""Watch a devnet: per-slot heads, forks, stalls, proposers and DKG epochs.

Every block finalized on the reference slot is fetched from every live slot and
its hash compared (a mismatch is a fork). The block's beneficiary is the
proposer's fee recipient, which names the proposing slot. The last block of each
epoch must carry the DKG outcome in its extra data; each one seen counts as a
completed ceremony. A stall is the highest finalized height not moving for
--stall seconds. Writes a JSON report with --report and exits 1 on a fork, a
stall or a slot whose finalized head lags the reference by more than --lag.

Usage: monitor.py --nodes nodes.json [--duration S] [--interval S] [--stall S]
                  [--skip slot,...] [--report FILE]
"""
import argparse
import json
import sys
import time
import urllib.request


def call(url, method, params):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
    req = urllib.request.Request(url, body, {"content-type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=3) as r:
            out = json.load(r)
    except Exception:
        return None
    return out.get("result")


def block(url, tag):
    b = call(url, "eth_getBlockByNumber", [tag, False])
    if not b:
        return None
    return {"number": int(b["number"], 16), "hash": b["hash"], "miner": b["miner"].lower(),
            "extra": (len(b["extraData"]) - 2) // 2, "time": int(b["timestamp"], 16)}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--nodes", required=True)
    ap.add_argument("--duration", type=float, default=60)
    ap.add_argument("--interval", type=float, default=2)
    ap.add_argument("--stall", type=float, default=30)
    ap.add_argument("--lag", type=int, default=50)
    ap.add_argument("--skip", default="")
    ap.add_argument("--report", default="")
    a = ap.parse_args()
    cfg = json.load(open(a.nodes))
    epoch = cfg["epoch_length"]
    skip = {int(s) for s in a.skip.split(",") if s != ""}
    slots = [s for s in cfg["slots"] if s["slot"] not in skip]
    by_fee = {s["fee_recipient"]: s["slot"] for s in cfg["slots"]}
    rep = {"epoch_length": epoch, "skipped": sorted(skip), "forks": [], "stalls": [],
           "proposers": {}, "boundaries": [], "start_finalized": None, "end_finalized": None,
           "checked_heights": 0, "missing": {}, "slots": {}}
    checked = None
    last_move, last_fin = time.time(), None
    began = time.time()
    while True:
        fins = {}
        for s in slots:
            f = block(s["rpc"], "finalized")
            h = block(s["rpc"], "latest")
            fins[s["slot"]] = f["number"] if f else None
            rep["slots"][str(s["slot"])] = {"finalized": fins[s["slot"]], "head": h["number"] if h else None}
        live = {k: v for k, v in fins.items() if v is not None}
        top = max(live.values()) if live else None
        if top is not None:
            if rep["start_finalized"] is None:
                rep["start_finalized"] = top
                checked = top
            if last_fin is None or top > last_fin:
                last_fin, last_move = top, time.time()
            rep["end_finalized"] = top
        if last_fin is not None and time.time() - last_move > a.stall:
            rep["stalls"].append({"at": time.time() - began, "finalized": last_fin})
            last_move = time.time()
        if top is not None:
            ref = max(live, key=lambda k: live[k])
            refurl = next(s["rpc"] for s in slots if s["slot"] == ref)
            while checked < top:
                checked += 1
                b = block(refurl, hex(checked))
                if b is None:
                    break
                slot = by_fee.get(b["miner"], b["miner"])
                rep["proposers"][str(slot)] = rep["proposers"].get(str(slot), 0) + 1
                if (checked + 1) % epoch == 0:
                    rep["boundaries"].append({"height": checked, "epoch": checked // epoch, "extra_bytes": b["extra"], "proposer": slot})
                for s in slots:
                    if s["slot"] == ref or live.get(s["slot"]) is None or live[s["slot"]] < checked:
                        continue
                    o = block(s["rpc"], hex(checked))
                    if o is None:
                        rep["missing"][str(s["slot"])] = rep["missing"].get(str(s["slot"]), 0) + 1
                    elif o["hash"] != b["hash"]:
                        rep["forks"].append({"height": checked, "ref_slot": ref, "ref": b["hash"], "slot": s["slot"], "hash": o["hash"]})
                rep["checked_heights"] += 1
        line = " ".join("%d:%s" % (k, v) for k, v in sorted(fins.items()))
        print("t=%4.0fs finalized %s forks=%d stalls=%d" % (time.time() - began, line, len(rep["forks"]), len(rep["stalls"])), flush=True)
        if time.time() - began >= a.duration:
            break
        time.sleep(a.interval)
    rep["duration_s"] = round(time.time() - began, 1)
    rep["lagging"] = [k for k, v in fins.items() if v is None or (top is not None and top - v > a.lag)]
    rep["ceremonies"] = sum(1 for b in rep["boundaries"] if b["extra_bytes"] > 0)
    rep["ok"] = not rep["forks"] and not rep["stalls"] and not rep["lagging"] and rep["end_finalized"] is not None and rep["end_finalized"] > rep["start_finalized"]
    out = json.dumps(rep, indent=1)
    if a.report:
        with open(a.report, "w") as f:
            f.write(out + "\n")
    print(json.dumps({k: rep[k] for k in ("ok", "start_finalized", "end_finalized", "checked_heights", "proposers", "ceremonies", "lagging")}))
    print("forks:", rep["forks"][:5], "stalls:", rep["stalls"][:5])
    sys.exit(0 if rep["ok"] else 1)


if __name__ == "__main__":
    main()
