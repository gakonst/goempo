sake_cups_vectors.json: produced by the Rust harness in harness/ (`cargo run --release -- vectors`, built on a 32-core build machine with tempo's Cargo.lock) against commonware-cryptography/stream 2026.10.0, the version tempo pins: SAKE dial/listen with ed25519 seeds 0x01*32 and 0x02*32, counter entropy starting at 10 and 100, namespace union_unique("TEMPO","_P2P"), then the first CUPS records each way.

harness/: the Rust harness source (not part of goempo; reference only). `vectors` prints the vectors above; `node` runs a commonware lookup network configured like tempo (SAKE V0, CUPS V0, channels 0-5 with tempo's rates) for interop runs.

interop-evidence.txt: output of the live interop runs (Go test TestInteropWithRustCommonware built with -tags interop against the harness "node" on localhost TCP): run A with Go dialing, run B with Rust dialing.

tempo-peers-evidence.txt and tempo_devnet_frames.json: TestPeerWithTempoValidators (-tags interop) holding slot 0 of a local 4-validator devnet of tempo c8b89f9 (genesis from tempo-xtask generate-localnet --seed 42, ports 31x00) whose other 3 validators are the Rust node: connection summary, and up to 3 raw channel payloads per (channel, validator) as received (channel 0 votes, 1 certificates, 3 broadcast, 5 dkg). Raw payloads include commonware's mux subchannel prefix.
