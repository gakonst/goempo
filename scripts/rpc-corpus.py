#!/usr/bin/env python3
# Build the R5 RPC corpus request list from cached mainnet data.
# Usage: scripts/rpc-corpus.py <blocks.jsonl> <statediff dir> <state-from> <state-to> > requests.jsonl
import glob, json, os, random, sys

blocks_path, statediff_dir, state_from, state_to = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
random.seed(5)
out = []

def req(method, params, block=None, live=False):
    out.append({"block": block, "live": live,
                "request": json.dumps({"jsonrpc": "2.0", "id": len(out) + 1, "method": method, "params": params}, separators=(",", ":"))})

def raw(body, live=False):
    out.append({"block": None, "live": live, "request": body})

def shape(x):
    if isinstance(x, dict):
        return "{" + ",".join(k + ":" + shape(v) for k, v in x.items()) + "}"
    if isinstance(x, list):
        return "[" + (shape(x[0]) if x else "") + "]"
    return "n" if x is None else "s"

blocks = [json.loads(l) for l in open(blocks_path)]
chosen, seen = [], set()
for b in blocks:
    for t in b["block"]["transactions"]:
        k = shape({k: v for k, v in t.items() if k not in ("input",)})
        if k not in seen:
            seen.add(k)
            if b not in chosen:
                chosen.append(b)
chosen += random.sample([b for b in blocks if b not in chosen], 12)
chosen.sort(key=lambda b: b["number"])

def block_methods(n, h, txs, block_tag=None):
    q = hex(n)
    for full in (False, True):
        req("eth_getBlockByNumber", [q, full])
        req("eth_getBlockByHash", [h, full])
    req("eth_getHeaderByNumber", [q])
    req("eth_getHeaderByHash", [h])
    req("eth_getBlockTransactionCountByNumber", [q])
    req("eth_getBlockTransactionCountByHash", [h])
    req("eth_getUncleCountByBlockNumber", [q])
    req("eth_getUncleCountByBlockHash", [h])
    req("eth_getUncleByBlockNumberAndIndex", [q, "0x0"])
    req("eth_getBlockReceipts", [q])
    req("eth_getBlockReceipts", [h])
    req("debug_getRawBlock", [q])
    req("debug_getRawHeader", [q])
    req("debug_getRawReceipts", [q])
    req("eth_getLogs", [{"fromBlock": q, "toBlock": q}])
    req("eth_getLogs", [{"blockHash": h}])
    req("eth_feeHistory", ["0x4", q, [10, 50, 90]])
    req("eth_getTransactionByBlockNumberAndIndex", [q, hex(len(txs))])
    for i, t in enumerate(txs[:6]):
        th = t["hash"]
        req("eth_getTransactionByHash", [th])
        req("eth_getTransactionReceipt", [th])
        req("eth_getRawTransactionByHash", [th])
        req("debug_getRawTransaction", [th])
        req("eth_getTransactionByBlockNumberAndIndex", [q, hex(i)])
        req("eth_getTransactionByBlockHashAndIndex", [h, hex(i)])
        req("eth_getRawTransactionByBlockNumberAndIndex", [q, hex(i)])

for b in chosen:
    blk = b["block"]
    block_methods(b["number"], blk["hash"], blk["transactions"])
    for r in b["receipts"][:3]:
        for lg in r["logs"][:1]:
            q = hex(b["number"])
            req("eth_getLogs", [{"fromBlock": q, "toBlock": q, "address": lg["address"]}])
            req("eth_getLogs", [{"fromBlock": q, "toBlock": q, "topics": [lg["topics"][0]]}])

diffs = {}
for f in sorted(glob.glob(os.path.join(statediff_dir, "*.jsonl"))):
    for l in open(f):
        d = json.loads(l)
        if state_from <= d["n"] <= state_to and any(t["post"] or t["pre"] for t in d["txs"]):
            diffs[d["n"]] = d
state_blocks = sorted(diffs)
picked = state_blocks if len(state_blocks) <= 40 else sorted(random.sample(state_blocks, 40))
def fetch_block(n):
    import urllib.request
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "eth_getBlockByNumber", "params": [hex(n), False]}).encode()
    r = urllib.request.Request("https://rpc.tempo.xyz", data=body, headers={"content-type": "application/json", "user-agent": "curl/8.5.0"})
    return json.loads(urllib.request.urlopen(r, timeout=60).read())["result"]

for n in picked:
    q = hex(n)
    blk = fetch_block(n)
    before = len(out)
    block_methods(n, blk["hash"], [{"hash": h} for h in blk["transactions"]])
    for o in out[before:]:
        o["block"] = n
    addrs, slots = [], []
    for t in diffs[n]["txs"]:
        for side in ("pre", "post"):
            for a, acct in t[side].items():
                if a not in addrs:
                    addrs.append(a)
                for k in (acct.get("storage") or {}):
                    if (a, k) not in slots:
                        slots.append((a, k))
    for a in addrs[:8]:
        req("eth_getBalance", [a, q], block=n)
        req("eth_getTransactionCount", [a, q], block=n)
        req("eth_getCode", [a, q], block=n)
        req("eth_getAccountInfo", [a, q], block=n)
    for a, k in slots[:12]:
        req("eth_getStorageAt", [a, k, q], block=n)
    if slots:
        req("eth_getStorageValues", [{slots[0][0]: [slots[0][1]]}, q], block=n)
    req("eth_getTransactionCount", [addrs[0], {"blockNumber": q}], block=n)
    token = "0x20c00000000000000000000016c6514b53947fdc"
    holder = "0x" + "0" * 24 + addrs[-1][2:]
    req("eth_call", [{"to": token, "data": "0x06fdde03"}, q], block=n)
    req("eth_call", [{"to": token, "data": "0x70a08231" + holder[2:]}, q], block=n)
    req("eth_estimateGas", [{"from": addrs[-1], "to": token, "data": "0x70a08231" + holder[2:]}, q], block=n)
out = [o for o in out if o.get("request")]

for m in ["eth_chainId", "net_version", "net_listening", "net_peerCount", "eth_protocolVersion", "eth_accounts",
          "eth_hashrate", "eth_mining", "eth_coinbase", "eth_getWork", "eth_maxPriorityFeePerGas", "eth_blobBaseFee",
          "eth_pendingTransactions", "eth_newPendingTransactionFilter", "eth_subscribe", "txpool_status", "txpool_content",
          "txpool_inspect", "eth_getFilterChanges", "eth_uninstallFilter", "foo_bar", "admin_peers", "trace_block"]:
    req(m, [])
for m in ["eth_blockNumber", "eth_gasPrice", "eth_syncing", "eth_capabilities", "tempo_forkSchedule", "web3_clientVersion", "eth_baseFee"]:
    req(m, [], live=True)
req("web3_sha3", ["0x68656c6c6f"])
req("web3_sha3", ["0x"])
req("eth_getFilterChanges", ["0x1"])
req("eth_uninstallFilter", ["0x1"])
req("eth_sendRawTransaction", ["0x"])
req("eth_sendRawTransaction", ["0x00"])
req("eth_sign", ["0x385193793fe875cd9f2341409563932023fb4fab", "0x00"])
req("eth_sendTransaction", [{}])
req("eth_feeHistory", ["0x0", "0xf1a", []])
req("eth_feeHistory", ["0x2", "0x1", []])
req("eth_feeHistory", ["0x3", "0xf1a", [50, 10]])
req("eth_feeHistory", ["0x3", "0xf1a"])
req("eth_getBalance", ["0x12", "0x1"])
req("eth_getBalance", ["0xfdaa73331d090687c51ef17290588688326ba2e7", "0xfffffffff"])
req("eth_getTransactionCount", ["0xfdaa73331d090687c51ef17290588688326ba2e7", "0xfffffffff"])
req("eth_getTransactionCount", ["0xfdaa73331d090687c51ef17290588688326ba2e7", "bogus"])
req("eth_getTransactionCount", ["0xfdaa73331d090687c51ef17290588688326ba2e7", {"blockHash": "0x1eacf62ae047ec7a6a9be10b74a106121fda9484523327e636a583588c99ea75"}])
req("eth_getCode", ["0xfdaa73331d090687c51ef17290588688326ba2e7", "0xfffffffff"])
req("eth_getStorageAt", ["0xfdaa73331d090687c51ef17290588688326ba2e7", "0xzz", "0xf1a"])
req("eth_getStorageAt", ["0xfdaa73331d090687c51ef17290588688326ba2e7", "0x0", "0xfffffffff"])
req("eth_getBlockByNumber", ["0xfffffffff", False])
req("eth_getBlockByNumber", ["0x12zz", False])
req("eth_getBlockByNumber", ["0xf1a"])
req("eth_getBlockByNumber", ["0xf1a", "true"])
req("eth_getBlockByNumber", ["1510", False])
req("eth_getBlockByNumber", [3866, False])
req("eth_getBlockByNumber", ["0x", False])
req("eth_getBlockByNumber", ["0xf1a", False, 3])
req("eth_getBlockByNumber", ["earliest", False])
req("eth_getBlockByNumber", ["pending", False])
req("eth_getBlockByHash", ["0x1eacf62ae047ec7a6a9be10b74a106121fda9484523327e636a583588c99ea75", False])
req("eth_getHeaderByHash", ["0x1eacf62ae047ec7a6a9be10b74a106121fda9484523327e636a583588c99ea75"])
req("eth_getTransactionByHash", ["0x12"])
req("eth_getTransactionByHash", ["0x1eacf62ae047ec7a6a9be10b74a106121fda9484523327e636a583588c99ea75"])
req("eth_getTransactionReceipt", ["0x1eacf62ae047ec7a6a9be10b74a106121fda9484523327e636a583588c99ea75"])
req("eth_getRawTransactionByHash", ["0x1eacf62ae047ec7a6a9be10b74a106121fda9484523327e636a583588c99ea75"])
req("eth_getTransactionByBlockNumberAndIndex", ["0xf1a", "0xzz"])
req("eth_getTransactionByBlockNumberAndIndex", ["0xf1a", "5"])
req("eth_getBlockReceipts", ["0xfffffffff"])
req("eth_getBlockTransactionCountByNumber", ["0xfffffffff"])
req("eth_getUncleCountByBlockNumber", ["0xfffffffff"])
req("eth_getLogs", [{"fromBlock": "0xf1a", "toBlock": "0xf10"}])
req("eth_getLogs", [{"blockHash": "0x1eacf62ae047ec7a6a9be10b74a106121fda9484523327e636a583588c99ea75"}])
req("debug_getRawHeader", ["0xfffffffff"])
req("debug_getRawBlock", ["0xfffffffff"])
req("debug_getRawReceipts", ["0xfffffffff"])
req("eth_getProof", ["0x20c00000000000000000000016c6514b53947fdc", ["0x0"], "0xf1a"])
req("eth_getAccount", ["0xfdaa73331d090687c51ef17290588688326ba2e7", "0xf1a"])
for body in ['{bad', '[]', '{"jsonrpc":"2.0","id":7}', '{"jsonrpc":"2.0","id":null,"method":"eth_chainId"}',
             '{"id":3,"method":"eth_chainId"}', '{"jsonrpc":"1.0","id":3,"method":"eth_chainId"}',
             '[{"jsonrpc":"2.0","id":1,"method":"eth_chainId"},5]', '{"jsonrpc":"2.0","method":"eth_chainId"}',
             '{"jsonrpc":"2.0","id":"abc","method":"eth_chainId"}', '{"jsonrpc":"2.0","id":1.5,"method":"eth_chainId"}',
             '{"jsonrpc":"2.0","id":[1],"method":"eth_chainId"}', '{"jsonrpc":"2.0","id":1,"method":5}',
             '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[1]}',
             '{"jsonrpc":"2.0","id":1,"method":"eth_getBlockByNumber","params":{"a":1}}',
             '[{"jsonrpc":"2.0","id":1,"method":"eth_chainId"},{"jsonrpc":"2.0","id":2,"method":"net_version"}]']:
    raw(body)
for b in chosen:
    for k in range(1, 4):
        if b["number"] >= k:
            req("debug_getRawBlock", [hex(b["number"] - k)])
            out[-1]["aux"] = True
for o in out:
    print(json.dumps(o, separators=(",", ":")))
