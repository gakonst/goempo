package p2p

import (
	"encoding/hex"
	"errors"
	"strings"

	"github.com/gakonst/goempo/internal/crypto/secp256k1"
)

var ErrEnode = errors.New("p2p: malformed enode URL")

type Node struct {
	ID      secp256k1.PublicKey
	Address string
}

func ParseEnode(url string) (Node, error) {
	var n Node
	rest, ok := strings.CutPrefix(url, "enode://")
	if !ok {
		return n, ErrEnode
	}
	id, address, ok := strings.Cut(rest, "@")
	if !ok || len(id) != 2*secp256k1.PublicKeyLength {
		return n, ErrEnode
	}
	raw, err := hex.DecodeString(id)
	if err != nil {
		return n, errors.Join(ErrEnode, err)
	}
	copy(n.ID[:], raw)
	if !n.ID.Valid() {
		return n, ErrEnode
	}
	address, _, _ = strings.Cut(address, "?")
	if !strings.Contains(address, ":") {
		return n, ErrEnode
	}
	n.Address = address
	return n, nil
}

func (n Node) String() string {
	return "enode://" + hex.EncodeToString(n.ID[:]) + "@" + n.Address
}
