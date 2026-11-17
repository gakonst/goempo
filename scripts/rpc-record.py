#!/usr/bin/env python3
# Record the reference node's answers for an RPC corpus request list.
# Usage: scripts/rpc-record.py <requests.jsonl> <corpus.jsonl> [url]
import json, sys, time, urllib.error, urllib.request

inp, outp = sys.argv[1], sys.argv[2]
url = sys.argv[3] if len(sys.argv) > 3 else "https://rpc.tempo.xyz"

def post(body):
    status, text = 0, ""
    for attempt in range(10):
        r = urllib.request.Request(url, data=body.encode(), headers={"content-type": "application/json", "user-agent": "curl/8.5.0"})
        try:
            resp = urllib.request.urlopen(r, timeout=60)
            status, text = resp.status, resp.read().decode()
        except urllib.error.HTTPError as e:
            status, text = e.code, e.read().decode()
        except Exception as e:
            status, text = 0, str(e)
        if status in (0, 429, 502, 503, 504) or '"code":-32005' in text[:300]:
            time.sleep(min(2 ** attempt, 30))
            continue
        return status, text
    return status, text

done = set()
try:
    for l in open(outp):
        done.add(json.loads(l)["request"])
except FileNotFoundError:
    pass
with open(outp, "a") as out:
    for l in open(inp):
        o = json.loads(l)
        if o["request"] in done:
            continue
        o["status"], o["response"] = post(o["request"])
        out.write(json.dumps(o, separators=(",", ":")) + "\n")
        out.flush()
        time.sleep(0.12)
