# scripts/devnet

A reproducible Tempo devnet: N validators (4 by default) from a fresh genesis and
DKG, run by the Rust reference node (tempoxyz/tempo), where any slot can be run by
goempo instead. It runs on a Linux host with cargo (dev-georgios, under
~/goempo-work/devnet); nothing here talks to mainnet or moderato.

## Files

- devnet.sh: build, gen, start, stop, wipe, status, monitor, quorum.
- agekey.py: decrypts the age (scrypt) signing keys tempo-xtask writes into the
  plaintext 0x-hex form both the Rust node and goempo read.
- nodes.py: writes NET/nodes.json, the slot table the monitor reads.
- monitor.py: per-slot heads, forks, stalls, proposers and DKG epochs; JSON report.

## Rust-only devnet

    scripts/devnet/devnet.sh build     # tempoxyz/tempo at TEMPO_REV, cargo build --release
    scripts/devnet/devnet.sh gen       # genesis + DKG for 4 validators, epoch 60
    scripts/devnet/devnet.sh start
    scripts/devnet/devnet.sh status
    scripts/devnet/devnet.sh monitor --duration 600 --report run.json
    scripts/devnet/devnet.sh stop

gen runs tempo-xtask generate-localnet --seed 42 --epoch-length 60 --validators
127.0.0.1:21000,...,127.0.0.1:21300. The genesis header's extra data holds the
initial DKG outcome (a Feldman-Desmedt deal over the 4 keys, threshold 3), every
hardfork is active at time 0, and the validators are written into
ValidatorConfigV2 at genesis with fee recipient equal to their onchain address,
so a block's beneficiary names the slot that proposed it.

Ports for slot i, with P = DEVNET_BASE_PORT + 100 i (base 21000): consensus p2p
P, execution devp2p P+1, consensus metrics P+2, authrpc P+3, discv5 P+4, JSON-RPC
(HTTP and WS) P+5, reth metrics P+6.

Each slot directory NET/127.0.0.1:P holds genesis-time keys: signing.key (age
encrypted, passphrase tempo-localnet-signing-key-secret), signing.key.hex
(plaintext), signing.share (BLS share), pubkey, enode.key, enode.identity, and at
run time data/ (Rust), goempo/ (goempo), node.log and pid.

## Mixed devnet

DEVNET_GOEMPO lists the slots goempo runs, with that slot's keys and share:

    DEVNET_GOEMPO=0 GOEMPO_BIN=~/goempo-work/devnet/bin/goempo scripts/devnet/devnet.sh start
    DEVNET_GOEMPO=0 scripts/devnet/devnet.sh quorum 1 120

goempo is started as goempo validator with tempo's flag names (--chain,
--datadir, --consensus.signing-key, --consensus.signing-share,
--consensus.listen-address, --consensus.metrics-address); DEVNET_GOEMPO_ARGS
overrides the argument template ({genesis} {datadir} {key} {share} {listen}
{peers} {http} {metrics} are substituted).

What each measurement proves, with 4 validators and threshold 3:

- goempo's votes are counted: quorum stops one Rust slot while goempo runs. The
  remaining two Rust validators cannot reach 3 votes alone, so finality that
  keeps advancing in that window needs goempo's notarize and finalize votes in
  the certificates.
- goempo's proposals are finalized: blocks whose beneficiary is goempo's fee
  recipient, finalized and identical on the Rust slots (monitor proposers).
- DKG with goempo as dealer and player: each epoch boundary block (height
  h with (h+1) % epoch == 0) must carry a new outcome in its extra data and the
  epoch after it keep finalizing with goempo in the committee (monitor
  ceremonies, with no stalls across boundaries).
- No forks or stalls: every finalized height compared across all live slots, and
  the highest finalized height never still for more than --stall seconds.

## Environment

DEVNET_WORK (~/goempo-work/devnet), TEMPO_REV (c8b89f9b6d), TEMPO_BIN, XTASK_BIN,
GOEMPO_BIN, DEVNET_NET, DEVNET_VALIDATORS (4), DEVNET_BASE_PORT (21000),
DEVNET_EPOCH_LENGTH (60), DEVNET_SEED (42), DEVNET_GOEMPO, DEVNET_GOEMPO_ARGS,
DEVNET_RUST_ARGS, DEVNET_JOBS (16).
