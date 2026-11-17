package validator

import (
	"encoding/binary"
	"errors"
	"math"
	"net/netip"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
)

const (
	NamespaceAdd    = "TEMPO_VALIDATOR_CONFIG_V2_ADD_VALIDATOR"
	NamespaceRotate = "TEMPO_VALIDATOR_CONFIG_V2_ROTATE_VALIDATOR"
	endpointMax     = math.MaxUint8
	endpointFields  = 2
	baseBytesMax    = u64Bytes + 2*addressBytes + endpointFields*(1+endpointMax)
	wordBytes       = 32
	selectorBytes   = 4
	addressBytes    = 20
	u64Bytes        = 8
)

const _ = uint(baseBytesMax - u64Bytes - 2*addressBytes - endpointFields*(1+endpointMax))

const (
	addHeadWords            = 6
	endpointPaddedMax       = (endpointMax + wordBytes) / wordBytes * wordBytes
	RegistrationCalldataMax = selectorBytes + addHeadWords*wordBytes + endpointFields*(wordBytes+endpointPaddedMax) + wordBytes + SignatureBytes
)

const _ = uint(endpointPaddedMax - endpointMax)

const _ = uint(RegistrationCalldataMax - endpointFields*endpointPaddedMax)

var (
	ErrIngress   = errors.New("validator: ingress is not ip:port")
	ErrEgress    = errors.New("validator: egress is not an ip")
	ErrSignature = errors.New("validator: signature does not verify")
)

var ValidatorConfigV2 = primitives.Address{0xcc, 0xcc, 0xcc, 0xcc, 19: 0x01}

var (
	selectorAdd    = selector("addValidator(address,bytes32,string,string,address,bytes)")
	selectorRotate = selector("rotateValidator(uint64,bytes32,string,string,bytes)")
)

func selector(sig string) [selectorBytes]byte {
	h := keccak.Sum256([]byte(sig))
	var out [selectorBytes]byte
	copy(out[:], h[:selectorBytes])
	return out
}

type Registration struct {
	ChainID   uint64
	Validator primitives.Address
	PublicKey [PublicKeyBytes]byte
	Ingress   string
	Egress    string
}

func (r Registration) Canonical() (Registration, error) {
	ap, err := netip.ParseAddrPort(r.Ingress)
	if err != nil {
		return Registration{}, ErrIngress
	}
	ip, err := netip.ParseAddr(r.Egress)
	if err != nil {
		return Registration{}, ErrEgress
	}
	if ap.Addr().Zone() != "" {
		return Registration{}, ErrIngress
	}
	if ip.Zone() != "" {
		return Registration{}, ErrEgress
	}
	out := r
	out.Ingress, out.Egress = ap.String(), ip.String()
	if len(out.Ingress) > endpointMax {
		return Registration{}, ErrIngress
	}
	if len(out.Egress) > endpointMax {
		return Registration{}, ErrEgress
	}
	return out, nil
}

func (r Registration) base() []byte {
	out := binary.BigEndian.AppendUint64(make([]byte, 0, baseBytesMax+addressBytes), r.ChainID)
	out = append(out, ValidatorConfigV2[:]...)
	out = append(out, r.Validator[:]...)
	out = append(out, byte(len(r.Ingress)))
	out = append(out, r.Ingress...)
	out = append(out, byte(len(r.Egress)))
	return append(out, r.Egress...)
}

func (r Registration) AddMessage(feeRecipient primitives.Address) [wordBytes]byte {
	return keccak.Sum256(append(r.base(), feeRecipient[:]...))
}

func (r Registration) RotateMessage() [wordBytes]byte { return keccak.Sum256(r.base()) }

func (r Registration) SignAdd(k SigningKey, feeRecipient primitives.Address) [SignatureBytes]byte {
	m := r.AddMessage(feeRecipient)
	return k.Sign(Namespace(NamespaceAdd), m[:])
}

func (r Registration) SignRotate(k SigningKey) [SignatureBytes]byte {
	m := r.RotateMessage()
	return k.Sign(Namespace(NamespaceRotate), m[:])
}

func (r Registration) CheckAdd(feeRecipient primitives.Address, sig []byte) error {
	m := r.AddMessage(feeRecipient)
	if !(Signed{Public: r.PublicKey, Namespace: Namespace(NamespaceAdd), Message: m[:], Signature: sig}).Verify() {
		return ErrSignature
	}
	return nil
}

func (r Registration) CheckRotate(sig []byte) error {
	m := r.RotateMessage()
	if !(Signed{Public: r.PublicKey, Namespace: Namespace(NamespaceRotate), Message: m[:], Signature: sig}).Verify() {
		return ErrSignature
	}
	return nil
}

func (r Registration) AddCalldata(feeRecipient primitives.Address, sig []byte) []byte {
	head := [][wordBytes]byte{addressWord(r.Validator), r.PublicKey, {}, {}, addressWord(feeRecipient), {}}
	return encodeCall(selectorAdd, head, []dynamic{{slot: 2, data: []byte(r.Ingress)}, {slot: 3, data: []byte(r.Egress)}, {slot: 5, data: sig}})
}

func (r Registration) RotateCalldata(index uint64, sig []byte) []byte {
	head := [][wordBytes]byte{u64Word(index), r.PublicKey, {}, {}, {}}
	return encodeCall(selectorRotate, head, []dynamic{{slot: 2, data: []byte(r.Ingress)}, {slot: 3, data: []byte(r.Egress)}, {slot: 4, data: sig}})
}

type dynamic struct {
	slot int
	data []byte
}

func addressWord(a primitives.Address) [wordBytes]byte {
	var w [wordBytes]byte
	copy(w[wordBytes-addressBytes:], a[:])
	return w
}

func u64Word(v uint64) [wordBytes]byte {
	var w [wordBytes]byte
	binary.BigEndian.PutUint64(w[wordBytes-u64Bytes:], v)
	return w
}

func padded(n int) int { return (n + wordBytes - 1) / wordBytes * wordBytes }

func encodeCall(sel [selectorBytes]byte, head [][wordBytes]byte, dyn []dynamic) []byte {
	tail := []byte{}
	for _, d := range dyn {
		head[d.slot] = u64Word(uint64(len(head)*wordBytes + len(tail)))
		length := u64Word(uint64(len(d.data)))
		tail = append(tail, length[:]...)
		body := make([]byte, padded(len(d.data)))
		copy(body, d.data)
		tail = append(tail, body...)
	}
	out := append([]byte{}, sel[:]...)
	for _, w := range head {
		out = append(out, w[:]...)
	}
	return append(out, tail...)
}
