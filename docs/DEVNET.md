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
RPC upstream and executing every block (cmd/goempo). What works for moderato
today (R6f dry run, 2026-11-17):

- Genesis: goempo-dev genesis <tempo>/crates/chainspec/src/genesis/moderato.json
  0x3952ff0c60a3f215ab11a3a2c7698126b7911da84faf5cb66bdbcb0c605738c4 prints
  R2 PASS (chain id 42431, 16 accounts, equal to moderato block 0's state root).
- Certificates: consensus.NewModeratoChain carries the testnet network
  identity (from_epoch 1747, the Rust reference's
  testnet-network-identity.json); TestModeratoNetworkIdentity checks the DKG
  outcome in moderato block 37735199 against it.
- Header chain with certificates: goempo-dev follow -chain moderato -upstream
  https://rpc.moderato.tempo.xyz -check https://rpc.moderato.tempo.xyz
  -last 3000 -steps 3 linked 3187 blocks to the tip (44676258), matched every
  hash and verified 3187 finalization certificates in 1m41s.
- Execution: the R1 sample executes 512 of 512 moderato blocks across every
  fork window (receipts and senders match).

Not done yet, needed before a goempo validator can run on moderato:

- cmd/goempo (the follower with execution) and goempo-dev replay are
  mainnet-only (genesis.Mainnet, MainnetChainID, NewMainnetChain). They need a
  chain switch (genesis, chain id, network identity, certify-from height).
- Moderato state at the tip: replay from genesis (44.7M blocks; mainnet's
  recent blocks replay at about 4.2k blocks/s, so most of a day plus fetching
  the chunks), then follow with execution across at least one epoch boundary.
- Validator mode from a synced datadir: goempo validator opens its datadir
  and catches up from peers; on moderato it must start from a moderato
  datadir at the tip, register the current epoch's outcome from the last
  boundary block (not epoch 0's), and join as a player with no share.
  The devnet has not yet exercised a validator added by addValidator after
  genesis (every devnet validator is in the genesis committee).

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
2. Keys: generate an ed25519 consensus key with goempo keys generate
   --output <file> (or tempo consensus generate-signing-key) and keep it
   offline-backed; goempo keys public --signing-key <file> prints its public
   key. Choose a validator address (an Ethereum account
   you control: it can later deactivate the validator or change its IPs and fee
   recipient) and a fee recipient.
3. Signature: prove control of the consensus key with
   tempo consensus create-add-validator-signature --validator-address <addr>
   --consensus.public-key <pubkey> --ingress <ip:port> --egress <ip>
   --fee-recipient <addr> --chain-id-from-rpc-url https://rpc.moderato.tempo.xyz
   --signing-key <file>, or goempo keys add-validator --signing-key <file>
   --chain-id 42431 --validator <addr> --ingress <ip:port> --egress <ip>
   --fee-recipient <addr> --simulate-rpc https://rpc.moderato.tempo.xyz,
   which prints the signature and addValidator call data and eth_calls it
   from the config owner (no transaction). It signs keccak256(chainId || 0xCCCC..0001 ||
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
- 2026-11-17 18:47 UTC, same binary with a T13 genesis (the default now; the
  first genesis activated T14 and T15, which goempo does not implement yet):
  R6e with goempo in 1 of 4 slots passed. Quorum window 431 to 565 with Rust
  slot 0 down; 1 hour soak 565 to 9703 with 0 forks, 0 stalls, 152 of 152
  DKG outcomes, 2328 goempo blocks finalized by the Rust validators, goempo's
  dealer log on chain in 162 of 162 epochs, 0 Rust ceremony failures, 0 WARNs
  naming goempo. One host-load spike (load average 106 on 32 cores from other
  builds) slowed blocks for a minute without a stall.
- 2026-11-17 19:53 UTC, goempo before 3c791cf in slots 2 and 3 (2 of 4): halted at
  t=807s with every slot at finalized 3590 (epoch 59, length 60). Root cause:
  block 3591 (Rust proposer) was notarized but its finalization did not
  complete before the next view, and the Rust leaders then proposed 3592 and
  on with 3591 as parent, as commonware's simplex allows (a proposal may
  extend a notarized, unfinalized block). goempo only built on and verified
  children of its finalized head, so it answered Undecided to every later
  proposal and never notarized them; with Rust slots 0 and 1 only 2 of 4
  votes remained, so nothing reached quorum again and views were nullified
  forever. With goempo in 1 of 4 slots the three Rust votes hid the bug.
  Fix 3c791cf: the engine walks from a proposal's parent back to the
  finalized head through the blocks it holds and node.Local executes that
  branch in a discarded overlay (WithBranch, ExecuteOn, DkgConfigOn) to build,
  verify and compute the boundary outcome; dealer logs in notarized branch
  blocks count and are not re-included. Regression tests drop finalize votes
  for one view (TestEngineBuildsOnNotarizedUnfinalizedParent halts on the old
  code at height 4) and for the second half of an epoch
  (TestEngineConcludesDKGOnNotarizedBranch). Logs saved under
  ~/goempo-work/devnet/r6e-halt on the build machine.
- 2026-11-17 20:40 UTC, rerun of 2 of 4: halted at finalized 2529 the same
  way (2530 notarized, not finalized, Rust building on it). The binary had not
  been replaced (copying over the running file failed), so it was still the
  old code; saved under ~/goempo-work/devnet/r6e-halt2-oldbin.
- 2026-11-17 21:07 UTC, goempo e3ac1c6 (with 3c791cf) in slots 2 and 3:
  R6e with 2 of 4 goempo passed. Quorum window 258 to 378 with Rust slot 0
  down (every certificate needed both goempo votes; goempo proposed 87 of the
  120 blocks); 1 hour soak 381 to 10259, 0 forks, 0 stalls, 165 of 165 DKG
  outcomes, 4930 goempo blocks finalized, both goempo dealer logs on chain in
  171 of 171 epochs, 0 Rust ceremony failures. Rust WARNs naming goempo: the
  paged-writer buffer notice when storing dealings (benign, also for Rust
  dealers) and 199 to 308 per pair of "serve send failed", tempo dropping
  marshal replies over its 8 per second per-peer quota during goempo backfill
  bursts; fixed in 3454772 (4 requests per peer per 500 ms).
