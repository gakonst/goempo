package statetest

import (
	"errors"
	"maps"
	"slices"
	"strconv"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
)

type Case struct {
	Name  string
	Fork  string
	Index int
}

type Outcome struct {
	Case    Case
	Skipped bool
	Passed  bool
	Reason  string
}

var (
	errIndex   = errors.New("statetest: post index out of range")
	errNoState = errors.New("statetest: account in post state is missing")
)

func RunFixture(name string, fx *Fixture) []Outcome {
	var out []Outcome
	for _, forkName := range slices.Sorted(maps.Keys(fx.Post)) {
		posts := fx.Post[forkName]
		for i := 0; i < len(posts); i++ {
			c := Case{Name: name, Fork: forkName, Index: i}
			out = append(out, runCase(fx, c))
		}
	}
	return out
}

func runCase(fx *Fixture, c Case) Outcome {
	fork, ok := evm.ParseFork(c.Fork)
	if !ok {
		return Outcome{Case: c, Skipped: true, Reason: "fork not supported"}
	}
	post := fx.Post[c.Fork][c.Index]
	reason, err := execute(fx, runInput{fork: fork, forkName: c.Fork, post: &post})
	if err != nil {
		return Outcome{Case: c, Reason: err.Error()}
	}
	return Outcome{Case: c, Passed: reason == "", Reason: reason}
}

type runInput struct {
	fork     evm.Fork
	forkName string
	post     *postJSON
}

func execute(fx *Fixture, in runInput) (string, error) {
	st, err := buildState(fx.Pre)
	if err != nil {
		return "", err
	}
	rules, err := buildRules(fx, in)
	if err != nil {
		return "", err
	}
	tx, err := buildTx(&fx.Transaction, in.post.Indexes)
	if err != nil {
		return "", err
	}
	outcome, txErr := apply(st, tx, rules)
	if in.post.ExpectException != "" {
		if txErr == nil {
			return "expected exception " + in.post.ExpectException + " but tx was valid", nil
		}
	} else if txErr != nil {
		return "tx rejected: " + txErr.Error(), nil
	}
	root := StateRoot(st)
	if root.Hex() != in.post.Hash {
		return "state root " + root.Hex() + " want " + in.post.Hash + " (status " + outcome.status.String() + ", gas " + strconv.FormatUint(outcome.gasUsed, 10) + ")" + diffState(st, in.post.State), nil
	}
	logs := LogsHash(outcome.logs)
	if logs.Hex() != in.post.Logs {
		return "logs hash " + logs.Hex() + " want " + in.post.Logs, nil
	}
	return "", nil
}

func buildRules(fx *Fixture, in runInput) (*chainRules, error) {
	chainID := u256.One()
	if fx.Config.ChainID != "" {
		id, err := parseInt(fx.Config.ChainID)
		if err != nil {
			return nil, err
		}
		chainID = id
	}
	block, err := buildBlock(fx.Env, in.fork)
	if err != nil {
		return nil, err
	}
	rules := &chainRules{fork: in.fork, config: evm.NewConfig(in.fork, chainID), block: block, maxBlobsPerTx: 6}
	if in.fork < evm.Cancun {
		return rules, nil
	}
	fraction := uint64(3338477)
	params, ok := fx.Config.BlobSchedule[in.forkName]
	if ok {
		fraction, err = parseUint64(params.BaseFeeUpdateFraction)
		if err != nil {
			return nil, err
		}
		limit, err := parseUint64(params.Max)
		if err != nil {
			return nil, err
		}
		rules.maxBlobsPerTx = int(min(limit, 64))
	}
	if in.fork >= evm.Osaka {
		rules.maxBlobsPerTx = min(rules.maxBlobsPerTx, osakaBlobsPerTx)
	}
	excess, err := parseUint64(fx.Env.ExcessBlobGas)
	if err != nil {
		return nil, err
	}
	rules.blobBaseFee, err = fakeExponential(expInput{factor: 1, numerator: excess, denominator: fraction})
	if err != nil {
		return nil, err
	}
	block.BlobBaseFee = rules.blobBaseFee
	return rules, nil
}

func pick(list []string, index int) (string, error) {
	if index < 0 || index >= len(list) {
		return "", errIndex
	}
	return list[index], nil
}

func buildTx(j *txJSON, idx indexesJSON) (*transaction, error) {
	tx := &transaction{}
	var err error
	tx.sender, err = parseAddress(j.Sender)
	if err != nil {
		return nil, err
	}
	tx.nonce, err = parseUint64(j.Nonce)
	if err != nil {
		return nil, err
	}
	gasLimit, err := pick(j.GasLimit, idx.Gas)
	if err != nil {
		return nil, err
	}
	tx.gasLimit, err = parseUint64(gasLimit)
	if err != nil {
		return nil, err
	}
	value, err := pick(j.Value, idx.Value)
	if err != nil {
		return nil, err
	}
	tx.value, err = parseInt(value)
	if err != nil {
		return nil, err
	}
	data, err := pick(j.Data, idx.Data)
	if err != nil {
		return nil, err
	}
	tx.data, err = parseBytes(data)
	if err != nil {
		return nil, err
	}
	if j.To == "" {
		tx.create = true
	} else {
		tx.to, err = parseAddress(j.To)
		if err != nil {
			return nil, err
		}
	}
	err = fillFees(tx, j)
	if err != nil {
		return nil, err
	}
	err = fillLists(tx, j, idx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func fillFees(tx *transaction, j *txJSON) error {
	var err error
	if j.GasPrice != "" {
		tx.gasPrice, err = parseInt(j.GasPrice)
		if err != nil {
			return err
		}
	}
	if j.MaxFeePerGas != "" {
		tx.kind = txDynamic
		tx.maxFee, err = parseInt(j.MaxFeePerGas)
		if err != nil {
			return err
		}
		tx.maxTip, err = parseInt(j.MaxPriorityFeePerGas)
		if err != nil {
			return err
		}
	}
	if j.MaxFeePerBlobGas != "" {
		tx.maxBlobFee, err = parseInt(j.MaxFeePerBlobGas)
		if err != nil {
			return err
		}
	}
	return nil
}

func fillLists(tx *transaction, j *txJSON, idx indexesJSON) error {
	if idx.Data < len(j.AccessLists) && j.AccessLists[idx.Data] != nil {
		if tx.kind == txLegacy {
			tx.kind = txAccessList
		}
		list := j.AccessLists[idx.Data]
		for i := 0; i < len(list); i++ {
			addr, err := parseAddress(list[i].Address)
			if err != nil {
				return err
			}
			t := accessTuple{address: addr}
			for k := 0; k < len(list[i].StorageKeys); k++ {
				key, err := parseHash(list[i].StorageKeys[k])
				if err != nil {
					return err
				}
				t.keys = append(t.keys, key)
			}
			tx.accessList = append(tx.accessList, t)
		}
	}
	if j.BlobVersionedHashes != nil {
		tx.kind = txBlob
		for i := 0; i < len(j.BlobVersionedHashes); i++ {
			h, err := parseHash(j.BlobVersionedHashes[i])
			if err != nil {
				return err
			}
			tx.blobHashes = append(tx.blobHashes, h)
		}
	}
	if j.AuthorizationList != nil {
		tx.kind = txSetCode
		for i := 0; i < len(j.AuthorizationList); i++ {
			a, err := parseAuth(&j.AuthorizationList[i])
			if err != nil {
				return err
			}
			tx.auths = append(tx.auths, a)
		}
	}
	return nil
}

func parseAuth(j *authJSON) (authorization, error) {
	var a authorization
	var err error
	a.chainID, err = parseInt(j.ChainID)
	if err != nil {
		return a, err
	}
	a.address, err = parseAddress(j.Address)
	if err != nil {
		return a, err
	}
	nonce, err := parseInt(j.Nonce)
	if err != nil {
		return a, err
	}
	a.nonce = ^uint64(0)
	if nonce.IsUint64() {
		a.nonce = nonce.Uint64()
	}
	parity := j.YParity
	if parity == "" {
		parity = j.V
	}
	a.yParity, err = parseInt(parity)
	if err != nil {
		return a, err
	}
	a.s, err = parseInt(j.S)
	if err != nil {
		return a, err
	}
	if j.Signer != "" {
		a.signer, err = parseAddress(j.Signer)
		if err != nil {
			return a, err
		}
		a.signed = true
	}
	return a, nil
}

func StateRoot(st *memstate.State) primitives.Hash {
	accounts := trie.NewSecure()
	addrs := st.Addresses()
	for i := 0; i < len(addrs); i++ {
		acc, ok := st.Get(addrs[i])
		if !ok {
			continue
		}
		storage := trie.NewSecure()
		for _, key := range sortedKeys(acc.Storage) {
			v := acc.Storage[key]
			b := v.Bytes32()
			storage.Update(key[:], rlp.EncodeBytes(primitives.TrimLeftZeros(b[:])))
		}
		storageRoot := storage.Hash()
		codeHash := keccak.Sum256(acc.Code)
		balance := acc.Balance.Bytes32()
		encoded := rlp.EncodeList(
			rlp.EncodeUint64(acc.Nonce),
			rlp.EncodeBytes(primitives.TrimLeftZeros(balance[:])),
			rlp.EncodeBytes(storageRoot[:]),
			rlp.EncodeBytes(codeHash[:]),
		)
		accounts.Update(addrs[i][:], encoded)
	}
	return accounts.Hash()
}

func LogsHash(logs []evm.Log) primitives.Hash {
	items := make([][]byte, 0, len(logs))
	for i := 0; i < len(logs); i++ {
		topics := make([][]byte, 0, len(logs[i].Topics))
		for t := 0; t < len(logs[i].Topics); t++ {
			topics = append(topics, rlp.EncodeBytes(logs[i].Topics[t][:]))
		}
		items = append(items, rlp.EncodeList(
			rlp.EncodeBytes(logs[i].Address[:]),
			rlp.EncodeList(topics...),
			rlp.EncodeBytes(logs[i].Data),
		))
	}
	return keccak.Sum256(rlp.EncodeList(items...))
}

func diffState(st *memstate.State, want map[string]accountJSON) string {
	out := ""
	seen := make(map[primitives.Address]bool)
	for _, k := range slices.Sorted(maps.Keys(want)) {
		addr, err := parseAddress(k)
		if err != nil {
			return out + "; bad address " + k
		}
		seen[addr] = true
		expect, err := parseAccount(want[k])
		if err != nil {
			return out + "; bad account " + k
		}
		got, ok := st.Get(addr)
		if !ok {
			out += "; missing " + k
			continue
		}
		out += diffAccount(k, accountPair{got: got, want: expect})
	}
	addrs := st.Addresses()
	for i := 0; i < len(addrs); i++ {
		if !seen[addrs[i]] {
			out += "; extra " + addrs[i].Hex()
		}
	}
	return out
}

type accountPair struct {
	got  memstate.Account
	want memstate.Account
}

func diffAccount(name string, p accountPair) string {
	out := ""
	if p.got.Balance != p.want.Balance {
		out += "; " + name + " balance " + p.got.Balance.Hex() + " want " + p.want.Balance.Hex()
	}
	if p.got.Nonce != p.want.Nonce {
		out += "; " + name + " nonce " + strconv.FormatUint(p.got.Nonce, 10) + " want " + strconv.FormatUint(p.want.Nonce, 10)
	}
	if string(p.got.Code) != string(p.want.Code) {
		out += "; " + name + " code differs"
	}
	for _, key := range sortedKeys(p.want.Storage) {
		if p.got.Storage[key] != p.want.Storage[key] {
			out += "; " + name + " slot " + key.Hex() + " = " + p.got.Storage[key].Hex() + " want " + p.want.Storage[key].Hex()
		}
	}
	for _, key := range sortedKeys(p.got.Storage) {
		_, ok := p.want.Storage[key]
		if !ok {
			out += "; " + name + " extra slot " + key.Hex()
		}
	}
	return out
}

func sortedKeys(m map[primitives.Hash]u256.Int) []primitives.Hash {
	keys := make([]primitives.Hash, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b primitives.Hash) int { return a.Compare(b) })
	return keys
}

var _ = errNoState
