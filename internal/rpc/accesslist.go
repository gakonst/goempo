package rpc

import (
	"encoding/json"

	"github.com/kapetan-io/tiger/assert"
)

type AccessListData struct {
	JSON json.RawMessage
	RLP  []byte
}

type AccessListSource interface {
	BlockAccessList(number uint64) (AccessListData, error)
}

func (s *Server) accessList(ref blockRef) (*AccessListData, error) {
	b := s.blockFor(ref)
	if b == nil {
		return nil, blockNotFound(ref.describe())
	}
	if b.Number() == 0 {
		return nil, blockNotFound(blockRef{kind: tagHash}.describe())
	}
	src, ok := s.exec.(AccessListSource)
	if !ok {
		return nil, internalError("unimplemented")
	}
	bal, err := src.BlockAccessList(b.Number())
	if err != nil {
		return nil, executionError(err)
	}
	return &bal, nil
}

type balArg uint8

const (
	balByID balArg = iota
	balByHash
	balByNumber
)

type balEncoding bool

const (
	balJSON balEncoding = false
	balRLP  balEncoding = true
)

func blockAccessListHandler(kind balArg, enc balEncoding) handler {
	return func(s *Server, p *params) ([]byte, error) {
		arg, err := p.required("block_id")
		if err != nil {
			return nil, err
		}
		var ref blockRef
		switch kind {
		case balByHash:
			h, err := hashParam(arg)
			if err != nil {
				return nil, err
			}
			ref = blockRef{kind: tagHash, hash: h}
		case balByNumber:
			ref, err = blockNumberParam(arg)
		case balByID:
			ref, err = blockIDParam(arg)
		default:
			assert.Unreachable("balArg: unhandled value")
		}
		if err != nil {
			return nil, err
		}
		if enc == balJSON && s.blockFor(ref) == nil {
			return resultNull, nil
		}
		bal, err := s.accessList(ref)
		if err != nil {
			return nil, err
		}
		if enc == balRLP {
			return dataResult(bal.RLP), nil
		}
		return bal.JSON, nil
	}
}
