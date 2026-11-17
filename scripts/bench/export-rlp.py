#!/usr/bin/env python3
"""Turn goempo-dev chunk files into an RLP block file for "tempo import".

A chunk (goempo-dev fetch-range) is a raw-deflate stream of 10,000 blocks,
each a 4-byte big-endian length followed by the block's RLP exactly as
debug_getRawBlock returned it. "tempo import" reads a file of concatenated
block RLPs, so the export only drops the length prefixes: both nodes then
execute byte-identical blocks.

  scripts/bench/export-rlp.py -chunks DIR -from 49410000 -to 49459999 -out blocks.rlp
"""
import argparse
import os
import struct
import zlib

CHUNK = 10_000


def read_chunk(path):
    with open(path, "rb") as f:
        data = f.read()
    if path.endswith(".blkz"):
        data = zlib.decompressobj(-15).decompress(data)
    off = 0
    while off < len(data):
        (n,) = struct.unpack_from(">I", data, off)
        off += 4
        yield data[off:off + n]
        off += n


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-chunks", required=True)
    ap.add_argument("-from", dest="lo", type=int, required=True)
    ap.add_argument("-to", dest="hi", type=int, required=True)
    ap.add_argument("-out", required=True)
    a = ap.parse_args()
    written = 0
    with open(a.out + ".tmp", "wb") as out:
        for start in range(a.lo // CHUNK * CHUNK, a.hi + 1, CHUNK):
            path = os.path.join(a.chunks, "%010d.blkz" % start)
            if not os.path.exists(path):
                path = os.path.join(a.chunks, "%010d.blk" % start)
            for i, raw in enumerate(read_chunk(path)):
                if a.lo <= start + i <= a.hi:
                    out.write(raw)
                    written += 1
    if written != a.hi - a.lo + 1:
        raise SystemExit("exported %d blocks, want %d" % (written, a.hi - a.lo + 1))
    os.rename(a.out + ".tmp", a.out)
    print("exported %d blocks to %s" % (written, a.out))


if __name__ == "__main__":
    main()
