#!/usr/bin/env python3
"""Report what each Rust slot saw from every other validator.

For each running Rust slot it scrapes the consensus metrics endpoint:
simplex batcher inbound_messages_total{peer,message} (votes and certificates
received, summed over epochs; counted on receipt, before verification),
latest_vote{peer} in the newest epoch (the highest view whose vote from that
peer the batcher took), dkg ceremony failures and blocks proposed by itself. It
then counts WARN lines in the slot's node.log naming each peer, which is where
commonware logs a peer being blocked and why (invalid signature, decoding
error, epoch mismatch, conflicting notarize), and prints the first few.

Usage: votes.py --nodes nodes.json [--slots 1,2,3] [--json FILE]
"""
import argparse
import json
import re
import urllib.request

LINE = re.compile(r'^([a-zA-Z_:]+)\{([^}]*)\} ([0-9.e+-]+)$')
LABEL = re.compile(r'(\w+)="([^"]*)"')
ANSI = re.compile(r'\x1b\[[0-9;]*m')


def scrape(url):
    try:
        with urllib.request.urlopen(url, timeout=3) as r:
            text = r.read().decode()
    except Exception:
        return None
    out = []
    for ln in text.splitlines():
        m = LINE.match(ln)
        if m:
            out.append((m.group(1), dict(LABEL.findall(m.group(2))), float(m.group(3))))
        elif ln and not ln.startswith("#"):
            parts = ln.split(" ")
            if len(parts) == 2:
                try:
                    out.append((parts[0], {}, float(parts[1])))
                except ValueError:
                    pass
    return out


def warns(path, keys, keep):
    found = {k: [] for k in keys}
    count = {k: 0 for k in keys}
    try:
        f = open(path, errors="replace")
    except OSError:
        return count, found
    with f:
        for ln in f:
            if ("WARN" not in ln and "ERROR" not in ln) or "buffer capacity is too low" in ln:
                continue
            for k in keys:
                if k in ln:
                    count[k] += 1
                    if len(found[k]) < keep:
                        found[k].append(ANSI.sub("", ln.strip())[:400])
    return count, found


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--nodes", required=True)
    ap.add_argument("--slots", default="")
    ap.add_argument("--keep", type=int, default=3)
    ap.add_argument("--json", default="")
    a = ap.parse_args()
    cfg = json.load(open(a.nodes))
    names = {s["public_key"]: s["slot"] for s in cfg["slots"]}
    want = {int(s) for s in a.slots.split(",") if s != ""}
    report = {}
    for s in cfg["slots"]:
        if want and s["slot"] not in want:
            continue
        m = scrape(s["consensus_metrics"])
        if m is None:
            report[s["slot"]] = {"error": "metrics unreachable"}
            continue
        inbound, latest, epochs, extra = {}, {}, set(), {}
        for name, labels, v in m:
            if name.endswith("simplex_batcher_inbound_messages_total"):
                p = names.get(labels.get("peer"), labels.get("peer"))
                inbound.setdefault(str(p), {})
                inbound[str(p)][labels["message"]] = inbound[str(p)].get(labels["message"], 0) + int(v)
            elif name.endswith("simplex_batcher_latest_vote"):
                e = int(labels.get("epoch", "0"))
                epochs.add(e)
                latest.setdefault(e, {})[str(names.get(labels.get("peer"), labels.get("peer")))] = int(v)
            elif name.endswith(("dkg_manager_ceremony_failures_total", "finalized_blocks_proposed_by_self_total",
                                "epoch_manager_latest_participants", "gossip_invalid_total",
                                "listener_handshakes_blocked_total")):
                extra[name.replace("consensus_engine_", "").replace("consensus_network_", "")] = int(v)
        newest = max(epochs) if epochs else None
        count, lines = warns(s["dir"] + "/node.log", [k for k in names if names[k] != s["slot"]], a.keep)
        report[s["slot"]] = {
            "inbound": inbound,
            "latest_vote_epoch": newest,
            "latest_vote": latest.get(newest, {}),
            "warn_lines_by_peer": {str(names[k]): c for k, c in count.items()},
            "warn_samples": {str(names[k]): v for k, v in lines.items() if v},
            **extra,
        }
    for slot, r in report.items():
        print("slot %s:" % slot)
        if "error" in r:
            print("  " + r["error"])
            continue
        print("  latest vote view per peer (epoch %s): %s" % (r["latest_vote_epoch"], r["latest_vote"]))
        for p, msgs in sorted(r["inbound"].items()):
            print("  from slot %s: %s" % (p, " ".join("%s=%d" % kv for kv in sorted(msgs.items()))))
        print("  warn lines naming peer: %s" % r["warn_lines_by_peer"])
        for p, v in r["warn_samples"].items():
            for ln in v:
                print("    [%s] %s" % (p, ln))
        print("  " + " ".join("%s=%d" % (k, v) for k, v in r.items() if isinstance(v, int)))
    if a.json:
        with open(a.json, "w") as f:
            json.dump(report, f, indent=1)


if __name__ == "__main__":
    main()
