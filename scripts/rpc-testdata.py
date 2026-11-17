#!/usr/bin/env python3
# Extract a small internal/rpc test corpus from the recorded corpus.
# Usage: scripts/rpc-testdata.py <blocks.jsonl> <corpus.jsonl> <outdir> <block>...
import json, sys
blocks_path, corpus_path, outdir = sys.argv[1], sys.argv[2], sys.argv[3]
want = set(int(x) for x in sys.argv[4:])
raws = {}
for l in open(blocks_path):
    b = json.loads(l)
    if b["number"] in want:
        raws[b["number"]] = b["raw"]
ents = [json.loads(l) for l in open(corpus_path)]
receipts, keep, cur = {}, [], None
for e in ents:
    if e.get("block") is not None or e.get("live") or e.get("aux"):
        continue
    try:
        r = json.loads(e["request"])
    except ValueError:
        r = None
    if isinstance(r, dict) and r.get("method") == "eth_getBlockByNumber" and r["params"] and r["params"][-1] is False and isinstance(r["params"][0], str) and r["params"][0].startswith("0x") and len(r["params"]) == 2:
        try:
            cur = int(r["params"][0], 16)
        except ValueError:
            cur = None
    if isinstance(r, dict) and r.get("method") == "debug_getRawReceipts" and cur in want:
        receipts[cur] = json.loads(e["response"])["result"]
    if cur in want and isinstance(r, dict) and r.get("method") not in ("eth_feeHistory",):
        keep.append(e)
with open(outdir + "/blocks.jsonl", "w") as f:
    for n in sorted(want):
        f.write(json.dumps({"number": n, "raw": raws[n], "receipts": receipts[n]}, separators=(",", ":")) + "\n")
with open(outdir + "/corpus.jsonl", "w") as f:
    for e in keep:
        f.write(json.dumps({"request": e["request"], "status": e["status"], "response": e["response"]}, separators=(",", ":")) + "\n")
print(len(keep), "entries")
