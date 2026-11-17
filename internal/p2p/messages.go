package p2p

import (
	"errors"

	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
)

const (
	BaseVersion  = 5
	codeHello    = 0x00
	codeDisc     = 0x01
	codePing     = 0x02
	codePong     = 0x03
	ethOffset    = 0x10
	capsMax      = 32
	ItemsMax     = 1024
	eth68        = 68
	eth69        = 69
	ethMessages  = 0x11
	eth69Message = 0x12
)

const _ = uint(ItemsMax - capsMax)

const (
	ethStatus           = 0x00
	ethGetBlockHeaders  = 0x03
	ethBlockHeaders     = 0x04
	ethGetBlockBodies   = 0x05
	ethBlockBodies      = 0x06
	ethBlockRangeUpdate = 0x11
)

var (
	ErrMalformed = errors.New("p2p: malformed message")
	ErrTooMany   = errors.New("p2p: message has too many items")
)

type Cap struct {
	Name    string
	Version uint64
}

type Hello struct {
	Version uint64
	Client  string
	Caps    []Cap
	Port    uint64
	ID      secp256k1.PublicKey
}

func (h Hello) Encode() []byte {
	e := rlp.NewEncoder(160)
	e.ListStart()
	e.WriteUint64(h.Version)
	e.WriteString(h.Client)
	e.ListStart()
	for _, c := range h.Caps {
		e.ListStart()
		e.WriteString(c.Name)
		e.WriteUint64(c.Version)
		e.ListEnd()
	}
	e.ListEnd()
	e.WriteUint64(h.Port)
	e.WriteBytes(h.ID[:])
	e.ListEnd()
	return append([]byte(nil), e.Encoded()...)
}

func DecodeHello(b []byte) (Hello, error) {
	var h Hello
	outer := rlp.NewStream(b)
	list, err := outer.List()
	if err != nil {
		return h, errors.Join(ErrMalformed, err)
	}
	if h.Version, err = list.Uint64(); err != nil {
		return h, errors.Join(ErrMalformed, err)
	}
	client, err := list.Bytes()
	if err != nil {
		return h, errors.Join(ErrMalformed, err)
	}
	h.Client = string(client)
	caps, err := list.List()
	if err != nil {
		return h, errors.Join(ErrMalformed, err)
	}
	n, err := caps.Count()
	if err != nil || n > capsMax {
		return h, errors.Join(ErrTooMany, err)
	}
	for range n {
		c, err := caps.List()
		if err != nil {
			return h, errors.Join(ErrMalformed, err)
		}
		name, err := c.Bytes()
		if err != nil {
			return h, errors.Join(ErrMalformed, err)
		}
		version, err := c.Uint64()
		if err != nil {
			return h, errors.Join(ErrMalformed, err)
		}
		h.Caps = append(h.Caps, Cap{Name: string(name), Version: version})
	}
	if h.Port, err = list.Uint64(); err != nil {
		return h, errors.Join(ErrMalformed, err)
	}
	if err := list.BytesFixed(h.ID[:]); err != nil {
		return h, errors.Join(ErrMalformed, err)
	}
	return h, nil
}

type DisconnectReason uint64

func decodeDisconnect(b []byte) DisconnectReason {
	s := rlp.NewStream(b)
	kind, err := s.Kind()
	if err != nil {
		return DisconnectReason(0xff)
	}
	if kind == rlp.KindString {
		v, err := s.Uint64()
		if err != nil {
			return DisconnectReason(0xff)
		}
		return DisconnectReason(v)
	}
	list, err := s.List()
	if err != nil || list.Empty() {
		return DisconnectReason(0xff)
	}
	v, err := list.Uint64()
	if err != nil {
		return DisconnectReason(0xff)
	}
	return DisconnectReason(v)
}

func encodeDisconnect(r DisconnectReason) []byte {
	return rlp.EncodeList(rlp.EncodeUint64(uint64(r)))
}

type Status struct {
	Version    uint64
	NetworkID  uint64
	TD         []byte
	Genesis    primitives.Hash
	Fork       ForkID
	Earliest   uint64
	Latest     uint64
	LatestHash primitives.Hash
}

func (s Status) Encode() []byte {
	e := rlp.NewEncoder(160)
	e.ListStart()
	e.WriteUint64(s.Version)
	e.WriteUint64(s.NetworkID)
	if s.Version == eth68 {
		e.WriteUintBytes(s.TD)
		e.WriteBytes(s.LatestHash[:])
		e.WriteBytes(s.Genesis[:])
		writeFork(e, s.Fork)
	} else {
		e.WriteBytes(s.Genesis[:])
		writeFork(e, s.Fork)
		e.WriteUint64(s.Earliest)
		e.WriteUint64(s.Latest)
		e.WriteBytes(s.LatestHash[:])
	}
	e.ListEnd()
	return append([]byte(nil), e.Encoded()...)
}

func writeFork(e *rlp.Encoder, f ForkID) {
	e.ListStart()
	e.WriteBytes(f.Hash[:])
	e.WriteUint64(f.Next)
	e.ListEnd()
}

func readFork(s *rlp.Stream) (ForkID, error) {
	var f ForkID
	list, err := s.List()
	if err != nil {
		return f, err
	}
	if err := list.BytesFixed(f.Hash[:]); err != nil {
		return f, err
	}
	if f.Next, err = list.Uint64(); err != nil {
		return f, err
	}
	return f, list.Finish()
}

func DecodeStatus(b []byte) (Status, error) {
	var st Status
	outer := rlp.NewStream(b)
	s, err := outer.List()
	if err != nil {
		return st, errors.Join(ErrMalformed, err)
	}
	if st.Version, err = s.Uint64(); err != nil {
		return st, errors.Join(ErrMalformed, err)
	}
	if st.NetworkID, err = s.Uint64(); err != nil {
		return st, errors.Join(ErrMalformed, err)
	}
	if st.Version == eth68 {
		err = st.decode68(&s)
	} else {
		err = st.decode69(&s)
	}
	if err != nil {
		return st, errors.Join(ErrMalformed, err)
	}
	return st, nil
}

func (st *Status) decode68(s *rlp.Stream) error {
	var err error
	if st.TD, err = s.UintBytes(32); err != nil {
		return err
	}
	if st.LatestHash, err = s.Hash(); err != nil {
		return err
	}
	if st.Genesis, err = s.Hash(); err != nil {
		return err
	}
	st.Fork, err = readFork(s)
	return err
}

func (st *Status) decode69(s *rlp.Stream) error {
	var err error
	if st.Genesis, err = s.Hash(); err != nil {
		return err
	}
	if st.Fork, err = readFork(s); err != nil {
		return err
	}
	if st.Earliest, err = s.Uint64(); err != nil {
		return err
	}
	if st.Latest, err = s.Uint64(); err != nil {
		return err
	}
	st.LatestHash, err = s.Hash()
	return err
}

type BlockRange struct {
	Earliest   uint64
	Latest     uint64
	LatestHash primitives.Hash
}

func DecodeBlockRange(b []byte) (BlockRange, error) {
	var r BlockRange
	outer := rlp.NewStream(b)
	s, err := outer.List()
	if err != nil {
		return r, errors.Join(ErrMalformed, err)
	}
	if r.Earliest, err = s.Uint64(); err != nil {
		return r, errors.Join(ErrMalformed, err)
	}
	if r.Latest, err = s.Uint64(); err != nil {
		return r, errors.Join(ErrMalformed, err)
	}
	if r.LatestHash, err = s.Hash(); err != nil {
		return r, errors.Join(ErrMalformed, err)
	}
	return r, nil
}

type HeadersRequest struct {
	ByHash  bool
	Hash    primitives.Hash
	Number  uint64
	Amount  uint64
	Skip    uint64
	Reverse bool
}

func encodeHeadersRequest(id uint64, r HeadersRequest) []byte {
	e := rlp.NewEncoder(64)
	e.ListStart()
	e.WriteUint64(id)
	e.ListStart()
	if r.ByHash {
		e.WriteBytes(r.Hash[:])
	} else {
		e.WriteUint64(r.Number)
	}
	e.WriteUint64(r.Amount)
	e.WriteUint64(r.Skip)
	if r.Reverse {
		e.WriteUint64(1)
	} else {
		e.WriteUint64(0)
	}
	e.ListEnd()
	e.ListEnd()
	return append([]byte(nil), e.Encoded()...)
}

type headersQuery struct {
	id      uint64
	request HeadersRequest
}

func decodeHeadersRequest(b []byte) (headersQuery, error) {
	var out headersQuery
	outer := rlp.NewStream(b)
	s, err := outer.List()
	if err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	if out.id, err = s.Uint64(); err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	q, err := s.List()
	if err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	r := &out.request
	origin, err := q.Bytes()
	if err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	if len(origin) == len(r.Hash) {
		r.ByHash = true
		copy(r.Hash[:], origin)
	} else if r.Number, err = rlp.DecodeUint64(rlp.EncodeBytes(origin)); err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	if r.Amount, err = q.Uint64(); err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	if r.Skip, err = q.Uint64(); err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	reverse, err := q.Uint64()
	if err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	r.Reverse = reverse == 1
	return out, nil
}

func encodeHashes(id uint64, hashes []primitives.Hash) []byte {
	e := rlp.NewEncoder(8 + 33*len(hashes))
	e.ListStart()
	e.WriteUint64(id)
	e.ListStart()
	for _, h := range hashes {
		e.WriteBytes(h[:])
	}
	e.ListEnd()
	e.ListEnd()
	return append([]byte(nil), e.Encoded()...)
}

type hashList struct {
	id     uint64
	hashes []primitives.Hash
}

func decodeHashes(b []byte) (hashList, error) {
	items, err := decodeItems(b)
	if err != nil {
		return hashList{}, err
	}
	out := hashList{id: items.id, hashes: make([]primitives.Hash, len(items.items))}
	for i, item := range items.items {
		raw, err := rlp.DecodeBytes(item)
		if err != nil || len(raw) != len(out.hashes[i]) {
			return hashList{}, errors.Join(ErrMalformed, err)
		}
		copy(out.hashes[i][:], raw)
	}
	return out, nil
}

func encodeItems(id uint64, items [][]byte) []byte {
	size := 16
	for _, it := range items {
		size += len(it)
	}
	e := rlp.NewEncoder(size)
	e.ListStart()
	e.WriteUint64(id)
	e.ListStart()
	for _, it := range items {
		e.WriteRaw(it)
	}
	e.ListEnd()
	e.ListEnd()
	return append([]byte(nil), e.Encoded()...)
}

type itemList struct {
	id    uint64
	items [][]byte
}

func decodeItems(b []byte) (itemList, error) {
	var out itemList
	outer := rlp.NewStream(b)
	s, err := outer.List()
	if err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	if out.id, err = s.Uint64(); err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	list, err := s.List()
	if err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	n, err := list.Count()
	if err != nil {
		return out, errors.Join(ErrMalformed, err)
	}
	if n > ItemsMax {
		return out, ErrTooMany
	}
	out.items = make([][]byte, 0, n)
	for range n {
		raw, err := list.Raw()
		if err != nil {
			return out, errors.Join(ErrMalformed, err)
		}
		out.items = append(out.items, raw)
	}
	return out, nil
}
