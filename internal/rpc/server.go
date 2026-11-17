package rpc

import (
	"maps"
	"slices"
	"strconv"

	"github.com/gakonst/goempo/internal/primitives"
)

const (
	MainnetChainID = 4217
	clientVersion  = "goempo/v0.1.0"
)

type Config struct {
	ChainID       uint64
	ClientVersion string
	ProofWindow   uint64
	GasCap        uint64
	GenesisHash   primitives.Hash
}

type Server struct {
	cfg     Config
	chain   Chain
	state   StateSource
	exec    Executor
	methods map[string]handler
	filters *filterTable
}

type handler func(s *Server, p *params) ([]byte, error)

func NewServer(cfg Config, chain Chain, state StateSource, exec Executor) *Server {
	if cfg.ChainID == 0 {
		cfg.ChainID = MainnetChainID
	}
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = clientVersion
	}
	if cfg.ProofWindow == 0 {
		cfg.ProofWindow = defaultProofWindow
	}
	if cfg.GasCap == 0 {
		cfg.GasCap = defaultGasCap
	}
	s := &Server{cfg: cfg, chain: chain, state: state, exec: exec, filters: newFilterTable()}
	s.methods = methodTable()
	addDebugStubs(s.methods)
	return s
}

func (s *Server) Methods() []string {
	return slices.Sorted(maps.Keys(s.methods))
}

func (s *Server) dispatch(c *call) ([]byte, error) {
	h, ok := s.methods[c.method]
	if !ok {
		return nil, methodNotFound()
	}
	p, err := newParams(c.params)
	if err != nil {
		return nil, err
	}
	return h(s, p)
}

func encode(f func(w *writer)) []byte {
	w := newWriter(responseHint)
	f(w)
	return w.bytes()
}

func quantityResult(v uint64) []byte {
	return encode(func(w *writer) { w.quantity(v) })
}

func dataResult(b []byte) []byte {
	return encode(func(w *writer) { w.data(b) })
}

func hashResult(h primitives.Hash) []byte { return dataResult(h[:]) }

func stringResult(v string) []byte { return appendQuoted(nil, v) }

func uintString(v uint64) string { return strconv.FormatUint(v, 10) }

var (
	resultNull  = []byte("null")
	resultTrue  = []byte("true")
	resultFalse = []byte("false")
	resultEmpty = []byte("[]")
)
