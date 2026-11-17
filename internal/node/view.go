package node

import (
	"errors"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/rpcexec"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

type StateView struct {
	n *Node
}

func (n *Node) StateView() *StateView { return &StateView{n: n} }

type headReader struct {
	n *Node
}

func (r headReader) Balance(addr primitives.Address) u256.Int {
	r.n.mu.Lock()
	defer r.n.mu.Unlock()
	return r.n.db.State().Balance(addr)
}

func (r headReader) Nonce(addr primitives.Address) uint64 {
	r.n.mu.Lock()
	defer r.n.mu.Unlock()
	return r.n.db.State().Nonce(addr)
}

func (r headReader) Code(addr primitives.Address) []byte {
	r.n.mu.Lock()
	defer r.n.mu.Unlock()
	return r.n.db.State().Code(addr)
}

func (r headReader) Storage(addr primitives.Address, key primitives.Hash) u256.Int {
	r.n.mu.Lock()
	defer r.n.mu.Unlock()
	return r.n.db.State().Storage(addr, key)
}

func (v *StateView) StateAt(number uint64) (rpc.StateReader, error) {
	v.n.mu.Lock()
	defer v.n.mu.Unlock()
	if number != v.n.head {
		return nil, ErrNoState
	}
	return headReader{n: v.n}, nil
}

func (v *StateView) ExecState(number uint64) (rpcexec.State, error) {
	if number != v.n.head {
		return nil, ErrNoState
	}
	return v.n.db.State(), nil
}

func (v *StateView) ProofAt(number uint64, addr primitives.Address, keys []primitives.Hash) (rpc.AccountProof, error) {
	v.n.mu.Lock()
	defer v.n.mu.Unlock()
	if number != v.n.head {
		return rpc.AccountProof{}, ErrNoState
	}
	p, err := v.n.db.Proof(addr, keys)
	if err != nil {
		return rpc.AccountProof{}, err
	}
	out := rpc.AccountProof{
		Exists:      p.Exists,
		Balance:     p.Balance,
		Nonce:       p.Nonce,
		CodeHash:    p.CodeHash,
		StorageRoot: p.StorageRoot,
		Proof:       p.AccountProof,
		Storage:     make([]rpc.StorageProof, len(p.Storage)),
	}
	for i, sp := range p.Storage {
		out.Storage[i] = rpc.StorageProof{Value: sp.Value, Proof: sp.Proof}
	}
	return out, nil
}

func (v *StateView) CodeByHash(h primitives.Hash) (rpc.CodeLookup, error) {
	v.n.mu.Lock()
	defer v.n.mu.Unlock()
	code, err := v.n.db.CodeByHash(h)
	if errors.Is(err, state.ErrCodeNotFound) {
		return rpc.CodeLookup{}, nil
	}
	if err != nil {
		return rpc.CodeLookup{}, err
	}
	return rpc.CodeLookup{Code: code, Found: true}, nil
}

func (n *Node) Server(cfg rpc.Config) *rpc.Server {
	view := n.StateView()
	if g, ok := n.recent.Block(0); ok && cfg.GenesisHash == (primitives.Hash{}) {
		cfg.GenesisHash = g.Hash
	}
	if cfg.ChainID == 0 {
		cfg.ChainID = rpc.MainnetChainID
	}
	exec := &rpcexec.Executor{ChainID: hardfork.MainnetChainID, Chain: n.recent, States: view, Blocks: n.exec, Lock: &n.mu}
	return rpc.NewServer(cfg, n.recent, view, exec)
}
