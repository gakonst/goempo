#!/usr/bin/env python3
"""Write the devnet's slot table (nodes.json) from tempo-xtask generate-localnet output.

Usage: nodes.py <net dir> <gen.log> <validators> <base port> <epoch length>
Each slot also gets a pubkey file (its ed25519 public key, hex) in its directory.
"""
import json
import re
import sys


def main():
    net, log, n, base, epoch = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5])
    text = open(log).read()
    found = re.findall(r"public key: (\S+)\s+onchain address: (\S+)\s+net address: (\S+)", text)
    by_addr = {addr: (pk, onchain) for pk, onchain, addr in found}
    slots = []
    for i in range(n):
        p = base + 100 * i
        addr = "127.0.0.1:%d" % p
        if addr not in by_addr:
            sys.exit("nodes.py: no validator for %s in %s" % (addr, log))
        pk, onchain = by_addr[addr]
        with open("%s/%s/pubkey" % (net, addr), "w") as f:
            f.write(pk + "\n")
        slots.append({
            "slot": i,
            "consensus": addr,
            "rpc": "http://127.0.0.1:%d" % (p + 5),
            "consensus_metrics": "http://127.0.0.1:%d/metrics" % (p + 2),
            "metrics": "http://127.0.0.1:%d/metrics" % (p + 6),
            "public_key": pk,
            "fee_recipient": onchain.lower(),
            "dir": "%s/%s" % (net, addr),
        })
    json.dump({"epoch_length": epoch, "slots": slots}, sys.stdout, indent=1)
    print()


if __name__ == "__main__":
    main()
