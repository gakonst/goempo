package node

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	genesisOptionalFields = 6
	localBlockSuffix      = ".block"
	localBlockBytesMax    = 64 << 20
	localReadGas          = 1 << 40
)

const _ = uint(localBlockBytesMax - archiveBlockBytes)

var (
	ErrLocalGenesis = errors.New("node: stored genesis differs from the genesis file")
	ErrLocalBlock   = errors.New("node: local block store is missing a block")
	ErrLocalParent  = errors.New("node: block does not extend the local head")
	ErrLocalRoot    = errors.New("node: block state root differs from execution")
)

func GenesisBlock(g *genesis.Genesis) types.Block {
	h := types.EthHeader{
		OmmersHash: keccak.Sum256(rlp.EncodeList()), Beneficiary: g.Coinbase, StateRoot: g.StateRoot(),
		TransactionsRoot: trie.OrderedRoot(nil), ReceiptsRoot: trie.OrderedRoot(nil),
		Difficulty: u256.FromBytes32(g.Difficulty), GasLimit: g.GasLimit, Timestamp: g.Timestamp,
		ExtraData: g.ExtraData, MixHash: g.MixHash, OptionalFields: genesisOptionalFields,
		BaseFeePerGas: g.BaseFee, WithdrawalsRoot: trie.OrderedRoot(nil), RequestsHash: sha256.Sum256(nil),
	}
	binary.BigEndian.PutUint64(h.Nonce[:], g.Nonce)
	return types.Block{Header: types.Header{Inner: h, TimestampMillisPart: g.Timestamp % 1000}, HasWithdrawals: true}
}

type LocalConfig struct {
	State        goio.FS
	StateOptions state.Options
	Blocks       goio.FS
	Genesis      *genesis.Genesis
}

type Local struct {
	mu     sync.Mutex
	db     *state.DB
	exec   *executor.Executor
	blocks goio.FS
	window [hashWindow]*types.Block
	head   *types.Block
}

func OpenLocal(cfg LocalConfig) (*Local, error) {
	db, err := state.Open(cfg.State, cfg.StateOptions)
	if err != nil {
		return nil, err
	}
	l, err := openLocal(db, cfg)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return l, nil
}

func epochLength(g *genesis.Genesis) (uint64, error) {
	var extra struct {
		EpochLength uint64 `json:"epochLength"`
	}
	err := json.Unmarshal(g.Config.Raw, &extra)
	return extra.EpochLength, err
}

func forkSchedule(g *genesis.Genesis) (hardfork.Schedule, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(g.Config.Raw, &fields); err != nil {
		return hardfork.Schedule{}, err
	}
	s := hardfork.NewSchedule()
	for i := int(hardfork.T0); i < len(s); i++ {
		f := hardfork.Fork(i)
		raw, ok := fields[hardfork.GenesisKey(f)]
		if !ok || string(raw) == "null" {
			continue
		}
		if err := json.Unmarshal(raw, &s[f]); err != nil {
			return hardfork.Schedule{}, err
		}
	}
	return s, nil
}

func registerForks(g *genesis.Genesis) error {
	id := hardfork.ChainID(g.Config.ChainID)
	if id == hardfork.MainnetChainID || id == hardfork.ModeratoChainID {
		return nil
	}
	s, err := forkSchedule(g)
	if err != nil {
		return err
	}
	return hardfork.Register(id, s)
}

func openLocal(db *state.DB, cfg LocalConfig) (*Local, error) {
	g := cfg.Genesis
	length, err := epochLength(g)
	if err != nil {
		return nil, err
	}
	if err := registerForks(g); err != nil {
		return nil, err
	}
	l := &Local{db: db, blocks: cfg.Blocks, exec: &executor.Executor{ChainID: hardfork.ChainID(g.Config.ChainID), EpochLength: length}}
	gb := GenesisBlock(g)
	if !db.Head().Loaded {
		if _, err := db.LoadGenesis(g); err != nil {
			return nil, err
		}
		if err := l.store(&gb); err != nil {
			return nil, err
		}
	}
	head := db.Head()
	first := head.Number - min(head.Number, hashWindow-1)
	for n := first; n <= head.Number; n++ {
		b, err := l.read(n)
		if err != nil {
			return nil, err
		}
		l.window[n%hashWindow] = b
		l.head = b
	}
	if l.head.Header.Inner.StateRoot != head.Root {
		return nil, ErrResumeRoot
	}
	stored, err := l.read(0)
	if err != nil {
		return nil, err
	}
	if stored.Hash() != gb.Hash() {
		return nil, ErrLocalGenesis
	}
	return l, nil
}

func blockName(n uint64) string { return fmt.Sprintf("%020d%s", n, localBlockSuffix) }

func (l *Local) store(b *types.Block) error {
	name := blockName(b.Header.Inner.Number)
	f, err := l.blocks.Create(name + ".tmp")
	if err != nil {
		return err
	}
	if _, err := f.Write(b.Encode()); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	return l.blocks.Rename(goio.Move{From: name + ".tmp", To: name})
}

func (l *Local) read(n uint64) (*types.Block, error) {
	f, err := l.blocks.Open(blockName(n))
	if err != nil {
		return nil, fmt.Errorf("%w: %d: %w", ErrLocalBlock, n, err)
	}
	defer f.Close()
	size := f.Size()
	if size <= 0 || size > localBlockBytesMax {
		return nil, fmt.Errorf("%w: %d", ErrLocalBlock, n)
	}
	raw := make([]byte, size)
	if _, err := f.ReadAt(raw, 0); err != nil {
		return nil, err
	}
	b, err := types.DecodeBlock(raw)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (l *Local) Close() error { return l.db.Close() }

func (l *Local) Executor() *executor.Executor { return l.exec }

func (l *Local) Head() *types.Block {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.head
}

func (l *Local) BlockByNumber(n uint64) (*types.Block, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > l.head.Header.Inner.Number {
		return nil, false
	}
	if b := l.window[n%hashWindow]; b != nil && b.Header.Inner.Number == n {
		return b, true
	}
	b, err := l.read(n)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (l *Local) getHash(n uint64) primitives.Hash {
	if b := l.window[n%hashWindow]; b != nil && b.Header.Inner.Number == n {
		return b.Hash()
	}
	return primitives.Hash{}
}

func (l *Local) GetHash(n uint64) primitives.Hash {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.getHash(n)
}

func (l *Local) WithHead(fn func(st *state.State, head *types.Block, getHash func(uint64) primitives.Hash) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.db.State()
	defer st.Discard()
	return fn(st, l.head, l.getHash)
}

func (l *Local) run(b *types.Block) (*state.State, error) {
	h := &b.Header.Inner
	if h.ParentHash != l.head.Hash() || h.Number != l.head.Header.Inner.Number+1 {
		return nil, ErrLocalParent
	}
	st := l.db.State()
	res, err := l.exec.Execute(st, executor.Input{Block: b, GetHash: l.getHash, Senders: blockSenders(b)})
	if err != nil {
		st.Discard()
		return nil, &BlockError{Number: h.Number, Field: "execution", Got: err.Error(), Want: "ok"}
	}
	if e := compareBlock(h, &res); e != nil {
		st.Discard()
		return nil, e
	}
	return st, nil
}

func (l *Local) Execute(b *types.Block) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, err := l.run(b)
	if err != nil {
		return err
	}
	defer st.Discard()
	root, err := st.PendingRoot()
	if err != nil {
		return err
	}
	if root != b.Header.Inner.StateRoot {
		return ErrLocalRoot
	}
	return nil
}

func (l *Local) Commit(b *types.Block) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, err := l.run(b)
	if err != nil {
		return err
	}
	root, err := st.PendingRoot()
	if err != nil {
		st.Discard()
		return err
	}
	if root != b.Header.Inner.StateRoot {
		st.Discard()
		return ErrLocalRoot
	}
	if err := l.store(b); err != nil {
		st.Discard()
		return err
	}
	if _, err := st.Commit(b.Header.Inner.Number); err != nil {
		return err
	}
	l.window[b.Header.Inner.Number%hashWindow] = b
	l.head = b
	return nil
}

func (l *Local) DkgConfig() (precompiles.DkgCeremonyConfig, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.db.State()
	defer st.Discard()
	h := l.head.Header.Inner
	c := precompiles.NewContext(st, hardfork.At(l.exec.ChainID, h.Timestamp), localReadGas)
	c.Block.Number = h.Number
	return precompiles.DkgCeremonyConfigV2(c, h.Number)
}
