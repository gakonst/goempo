# Devnet and moderato readiness

How goempo is tested as a Tempo validator next to the Rust reference node, and
what is left before it can validate on moderato (testnet, chain 42431). The
harness itself is scripts/devnet (see its README for commands and ports).

## Rust-only devnet (baseline)

Built from tempoxyz/tempo at c8b89f9b6d (tempo 1.18.4-dev), on a 32-core build machine
under ~/goempo-work/devnet:

    scripts/devnet/devnet.sh build && scripts/devnet/devnet.sh gen && scripts/devnet/devnet.sh start
    scripts/devnet/devnet.sh monitor --duration 900 --stall 15 --report reports/rust-only-900s.json

Four validators on 127.0.0.1:21000..21300, genesis from tempo-xtask
generate-localnet (seed 42, epoch length 60, every hardfork at time 0, the
initial DKG outcome in the genesis extra data, the validators in
ValidatorConfigV2 from genesis). It produces about 2 blocks/s, each finalized
within the next block, and every boundary block carries a 684-byte DKG outcome
(a reshare per epoch, 0 ceremony failures). The 4 validators propose in turn.

Measured 2026-11-17 (reports/rust-only-900s.json): 900 s, finalized 292 to
2072, all 1780 heights compared on all 4 slots with 0 forks, no stall over 15 s,
30 of 30 boundary blocks carrying a DKG outcome, proposers 446/423/446/465.

Quorum check on a second net (base port 31000): with slot 1 stopped for 60 s the
other 3 kept finalizing (52 to 165, no stall); with slots 1 and 2 stopped,
finality stayed at 725 for the 45 s window (2 stalls) and resumed when they
returned. So with n = 4 any window of progress with 2 Rust validators down needs
the third signer, which is what makes the quorum test below a proof that
goempo's votes are counted.

Seen once on the Rust-only net, under a host load of 46 on 32 cores: a 40 s
liveness gap at height 80 (epoch 1, views 22 to 34 all nullified although each
leader built its proposal). The monitor's stall threshold must allow for it; a
stall on a mixed net is only goempo's fault when the Rust-only baseline on the
same host shows none over the same window.

## Mixed devnet: what counts as passing (R6e)

Run with DEVNET_GOEMPO=0 (then 0,1) so goempo takes slot 0's keys, share and
ports; the Rust slots keep theirs. With n = 4 the quorum is 3:

| Claim | Evidence the harness collects |
| --- | --- |
| goempo's votes are counted | quorum 1 120: with Rust slot 1 stopped, finality must keep advancing; only 2 Rust signers remain, so every certificate in that window holds goempo's partial signature. votes shows slot 0's Notarize/Finalize counts and latest vote view on each Rust slot, and any WARN where a Rust slot blocked goempo (invalid signature, decoding error, epoch mismatch). |
| goempo's proposals are finalized | monitor proposers: blocks whose beneficiary is slot 0's fee recipient, finalized with the same hash on every Rust slot. |
| DKG with goempo as dealer and player | monitor ceremonies: every boundary block carries a new outcome, and the epochs after it finalize with goempo signing (which needs the share goempo derived as a player); Rust slots' dkg_manager_ceremony_failures_total stays 0, and their logs show goempo's dealing acknowledged. |
| No forks or stalls over an hour | monitor --duration 3600 --stall 30: every finalized height compared across all slots, 0 forks, 0 stalls beyond the baseline. |

## Moderato: following

Moderato at 2026-11-17: height 44.66M, about 1.85 blocks/s, epoch length
21600 (about 3.2 h), 22 active validators, all in the committee and all DKG
dealers (tempo consensus info --rpc-url https://rpc.moderato.tempo.xyz, read
only). ValidatorConfigV2 (0xCCCCCCCC00000000000000000000000000000001) owner is
0xe823bb1ae947a0197929a0ea5fc49394ca621483.

goempo follows a chain by fetching blocks and finalization certificates from an
RPC upstream and executing every block (cmd/goempo). For moderato it needs
the moderato genesis (internal/genesis already reproduces its state root) and
the chain id and initial committee from it instead of mainnet's; then:

    goempo --chain moderato --upstream https://rpc.moderato.tempo.xyz --datadir ~/goempo-moderato --archive ''

Passive consensus peering is not possible for a non-validator: commonware's
authenticated lookup network admits a connection only from a public key in a
registered peer set (tracker directory eligible), and Tempo registers only
ValidatorConfigV2 validators. The Rust node's own non-validator mode (tempo
node --follow) also syncs over RPC, not consensus p2p. So following over RPC is
the passive mode; consensus peering starts only after registration.

## Moderato: registering a goempo validator (for a human, not done by agents)

Agents must not do any of this; it changes a shared network.

1. Machine: a public IPv4 host with a stable address, the consensus port (for
   example 8000/tcp) reachable from the other validators' egress addresses,
   and goempo synced to the moderato tip (as above), keeping up with 1.85
   blocks/s.
2. Keys: generate an ed25519 consensus key (tempo consensus
   generate-signing-key, or goempo's validator key tool once it exists) and keep
   it offline-backed; print its public key with tempo consensus
   show-verification-key. Choose a validator address (an Ethereum account
   you control: it can later deactivate the validator or change its IPs and fee
   recipient) and a fee recipient.
3. Signature: prove control of the consensus key with
   tempo consensus create-add-validator-signature --validator-address <addr>
   --consensus.public-key <pubkey> --ingress <ip:port> --egress <ip>
   --fee-recipient <addr> --chain-id-from-rpc-url https://rpc.moderato.tempo.xyz
   --signing-key <file>. It signs keccak256(chainId || 0xCCCC..0001 ||
   validatorAddress || len(ingress) || ingress || len(egress) || egress ||
   feeRecipient) under the add-validator namespace. The ingress must be unique
   among active validators.
4. Ask Tempo: addValidator is owner-only. Send the address, public key,
   ingress, egress, fee recipient and signature to the Tempo team, who submit
   it from the owner account (tempo consensus add-validator --rpc-url
   https://rpc.moderato.tempo.xyz ...). Agents never submit it.
5. Start goempo in validator mode with the consensus key and no share:
   it joins the next DKG ceremony as a player (shares are dealt at the epoch
   boundary after the add), signs from the epoch after that, and becomes a
   dealer in later ceremonies. Watch tempo consensus info for is_dkg_player,
   in_committee and is_dkg_dealer turning true, and the other validators'
   ceremony failures staying at 0.
6. To leave: deactivateValidator from the validator address (or the owner).

Before step 4, goempo should have passed R6e on the devnet (an hour with 2 of
4 goempo validators, no forks or stalls) and followed moderato for at least
one full epoch boundary (about 3.2 h) with every block's state root matching.

## R6e run log

- 2026-11-17 18:45 UTC, goempo d1a3576 in slot 3, Rust c8b89f9b6d in slots
  0 to 2, fresh chain: goempo exited before importing block 1 with "node: block
  state root differs from execution". Block 1 from the Rust proposer is empty
  (0 transactions, gas 0) yet changes the state root (0xf299ab91...e536 to
  0xa34d5e74...2c13); the visible change is the EIP-2935 history contract
  (0x0000F90827F1C53a10cb7A02335B175320002935) slot 0 set to the genesis hash,
  the Prague pre-block system call. Rust slots kept finalizing without it.
  Sent to the validator agent with the raw blocks.
