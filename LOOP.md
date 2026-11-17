# LOOP: one hill-climb iteration

Each iteration moves the frontier up by one measured step, and never down.

1. **Measure.** `git pull`, then `go run ./cmd/goempo-dev score -json > /tmp/score.json`. Compare with the last row of FRONTIER.jsonl.
2. **Pick the lowest rung that is not at its frontier** (GOAL.md). On R3 the work is the first block that does not match: `goempo-dev replay` names it and the field that differs.
3. **Find the cause, not the symptom.** For a block that does not match, diff its execution against `debug_traceBlockByNumber` (opcode, gas, stack) and the reference's code. Read the code; don't guess.
4. **Fix it with a test that fails before and passes after.** Store the block or transaction as a fixture under `testdata/` when it is small.
5. **Gate.** `scripts/verify.sh` passes. `tiger check` is never silenced: rewrite the code until it passes. An escape directive needs a reason and lowers nobody's budget.
6. **Prove it went up.** Run the score again. If a rung went down, revert.
7. **Record and push.** Append the score as one line to FRONTIER.jsonl, update FRONTIER.md, commit with a message that names the rung, the old and new score and the cause, and push to `main`.

Work in parallel by rung or by package, never two agents in one package. Each agent works in its own git worktree and rebases before pushing. Long runs (replay over millions of blocks, fuzzing, block fetches) go on a box, not on the dev host.
