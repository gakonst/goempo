import json, sys, urllib.request
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
L = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18545"
U = sys.argv[2] if len(sys.argv) > 2 else "http://100.98.157.16:8545"
def call(url, m, p):
    req = urllib.request.Request(url, json.dumps({"jsonrpc":"2.0","id":1,"method":m,"params":p}).encode(), {"content-type":"application/json"})
    r = json.load(opener.open(req, timeout=60))
    return r.get("result", r.get("error"))
head = int(call(L, "eth_blockNumber", []), 16)
N = hex(head)
blk = call(U, "eth_getBlockByNumber", [N, True])
addrs = {blk["miner"]}
for tx in blk["transactions"][:6]:
    addrs.add(tx["from"])
    if tx.get("to"): addrs.add(tx["to"])
checks = [("eth_blockNumber", []), ("eth_getBlockByNumber", [N, True]), ("eth_getBlockReceipts", [N]), ("eth_chainId", [])]
for a in sorted(addrs):
    checks += [("eth_getBalance", [a, N]), ("eth_getTransactionCount", [a, N]), ("eth_getCode", [a, N]), ("eth_getProof", [a, ["0x0"], N])]
    checks += [("eth_getStorageAt", [a, "0x0", N])]
for tx in blk["transactions"][:3]:
    checks += [("eth_getTransactionReceipt", [tx["hash"]]), ("debug_traceTransaction", [tx["hash"], {"tracer": "callTracer"}])]
ok = bad = 0
for m, p in checks:
    a, b = call(L, m, p), call(U, m, p)
    if a == b:
        ok += 1
    else:
        bad += 1
        print("DIFF", m, json.dumps(p)[:120], "\n  goempo:", json.dumps(a)[:300], "\n  ref:   ", json.dumps(b)[:300])
print(f"head {head}: {ok}/{ok+bad} equal")
