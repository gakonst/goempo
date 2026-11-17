#!/usr/bin/env python3
"""Per epoch: which validators' DKG dealer logs reached the chain.

Tempo validators publish their signed dealer log in the extraData of a block
they propose mid-epoch; the last block of the epoch carries the outcome. This
walks finalized blocks on one slot's RPC and, per epoch, lists the slots whose
proposed blocks carried a non-empty extraData before the boundary (dealer logs
on chain) and the outcome size at the boundary.

Usage: dealers.py --nodes nodes.json [--slot 1] [--from 1] [--to H]
"""
import argparse
import json
import urllib.request


def call(url, method, params):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
    req = urllib.request.Request(url, body, {"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.load(r).get("result")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--nodes", required=True)
    ap.add_argument("--slot", type=int, default=1)
    ap.add_argument("--from", dest="start", type=int, default=1)
    ap.add_argument("--to", type=int, default=0)
    ap.add_argument("--json", default="")
    a = ap.parse_args()
    cfg = json.load(open(a.nodes))
    epoch = cfg["epoch_length"]
    url = cfg["slots"][a.slot]["rpc"]
    by_fee = {s["fee_recipient"]: s["slot"] for s in cfg["slots"]}
    top = a.to or int(call(url, "eth_getBlockByNumber", ["finalized", False])["number"], 16)
    epochs = {}
    for h in range(a.start, top + 1):
        b = call(url, "eth_getBlockByNumber", [hex(h), False])
        n = (len(b["extraData"]) - 2) // 2
        e = epochs.setdefault(h // epoch, {"dealer_logs": {}, "outcome_bytes": None})
        slot = by_fee.get(b["miner"].lower(), b["miner"])
        if (h + 1) % epoch == 0:
            e["outcome_bytes"] = n
        elif n > 0:
            e["dealer_logs"][str(slot)] = e["dealer_logs"].get(str(slot), 0) + 1
    for k in sorted(epochs):
        e = epochs[k]
        print("epoch %d: dealer logs from slots %s, outcome %s bytes" % (k, sorted(e["dealer_logs"]), e["outcome_bytes"]))
    if a.json:
        with open(a.json, "w") as f:
            json.dump(epochs, f, indent=1)


if __name__ == "__main__":
    main()
