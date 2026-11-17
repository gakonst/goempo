#!/usr/bin/env python3
"""Decrypt an age scrypt (passphrase) file and print its plaintext as 0x hex.

tempo-xtask generate-localnet writes each validator's ed25519 signing key as an
age payload encrypted with a fixed localnet passphrase. The Rust node also takes
a plaintext key (0x hex of the 32 encoded private key bytes) when
--consensus.secret is absent, and that plaintext form is what goempo reads.

Usage: agekey.py <signing.key> <passphrase>
Needs only the python cryptography package.
"""
import base64
import hashlib
import hmac
import sys

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305
from cryptography.hazmat.primitives.kdf.hkdf import HKDF
from cryptography.hazmat.primitives.kdf.scrypt import Scrypt

CHUNK = 64 * 1024


def b64(s):
    return base64.b64decode(s + "=" * (-len(s) % 4))


def hkdf(ikm, salt, info):
    return HKDF(algorithm=hashes.SHA256(), length=32, salt=salt, info=info).derive(ikm)


def decrypt(data, passphrase):
    header_end = data.index(b"\n---")
    mac_line_end = data.index(b"\n", header_end + 1)
    header = data[:header_end].decode().split("\n")
    if header[0] != "age-encryption.org/v1":
        raise ValueError("not an age v1 file")
    stanza = header[1].split(" ")
    if stanza[:2] != ["->", "scrypt"] or len(header) < 3:
        raise ValueError("not a scrypt recipient stanza")
    salt, log_n = b64(stanza[2]), int(stanza[3])
    body = b64("".join(header[2:]))
    wrap = Scrypt(salt=b"age-encryption.org/v1/scrypt" + salt, length=32,
                  n=1 << log_n, r=8, p=1).derive(passphrase.encode())
    file_key = ChaCha20Poly1305(wrap).decrypt(bytes(12), body, None)
    mac_line = data[header_end + 1:mac_line_end].decode()
    mac = b64(mac_line.split(" ")[1])
    want = hmac.new(hkdf(file_key, b"", b"header"), data[:header_end + 4], hashlib.sha256).digest()
    if not hmac.compare_digest(mac, want):
        raise ValueError("header MAC mismatch")
    payload = data[mac_line_end + 1:]
    key = ChaCha20Poly1305(hkdf(file_key, payload[:16], b"payload"))
    rest, out, counter = payload[16:], b"", 0
    while True:
        chunk, rest = rest[:CHUNK + 16], rest[CHUNK + 16:]
        last = len(rest) == 0
        nonce = counter.to_bytes(11, "big") + (b"\x01" if last else b"\x00")
        out += key.decrypt(nonce, chunk, None)
        if last:
            return out
        counter += 1


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: agekey.py <file> <passphrase>")
    with open(sys.argv[1], "rb") as f:
        print("0x" + decrypt(f.read(), sys.argv[2]).hex())


if __name__ == "__main__":
    main()
