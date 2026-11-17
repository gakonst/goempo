package precompiles

import (
	"errors"
	"strconv"
)

var errIPSyntax = errors.New("ip syntax")

const (
	ipv6Groups    = 8
	ipTextMax     = 64
	ipv6GroupText = 4
)

const _ = uint(ipTextMax - ipv6Groups*(ipv6GroupText+1))

type ipAddr struct {
	v6    flag
	bytes [16]byte
}

type socketAddr struct {
	ip    ipAddr
	port  uint16
	scope uint32
}

type ipParser struct {
	in  []byte
	pos int
}

type numberSpec struct {
	radix      uint64
	maxDigits  int
	zeroPrefix flag
	limit      uint64
}

var (
	specOctet = numberSpec{radix: 10, maxDigits: 3, limit: 0xff}
	specGroup = numberSpec{radix: 16, maxDigits: 4, zeroPrefix: true, limit: 0xffff}
	specPort  = numberSpec{radix: 10, zeroPrefix: true, limit: 0xffff}
	specScope = numberSpec{radix: 10, zeroPrefix: true, limit: 0xffffffff}
)

func (p *ipParser) peek() int {
	if p.pos >= len(p.in) {
		return -1
	}
	return int(p.in[p.pos])
}

func (p *ipParser) readChar(ch byte) bool {
	if p.peek() != int(ch) {
		return false
	}
	p.pos++
	return true
}

func digitValue(ch int) int {
	switch {
	case ch >= '0' && ch <= '9':
		return ch - '0'
	case ch >= 'a' && ch <= 'f':
		return ch - 'a' + 10
	case ch >= 'A' && ch <= 'F':
		return ch - 'A' + 10
	}
	return 99
}

func (p *ipParser) fail(start int) error {
	p.pos = start
	return errIPSyntax
}

func (p *ipParser) readNumber(s numberSpec) (uint64, error) {
	start := p.pos
	leadingZero := p.peek() == '0'
	result := uint64(0)
	count := 0
	for _, ch := range p.in[start:] {
		d := digitValue(int(ch))
		if uint64(d) >= s.radix {
			break
		}
		p.pos++
		result = result*s.radix + uint64(d)
		count++
		if result > s.limit {
			return 0, p.fail(start)
		}
		if s.maxDigits > 0 && count > s.maxDigits {
			return 0, p.fail(start)
		}
	}
	if count == 0 {
		return 0, p.fail(start)
	}
	if !bool(s.zeroPrefix) && leadingZero && count > 1 {
		return 0, p.fail(start)
	}
	return result, nil
}

func (p *ipParser) readIPv4() ([4]byte, error) {
	start := p.pos
	var out [4]byte
	for i := range out {
		if i > 0 && !p.readChar('.') {
			return out, p.fail(start)
		}
		v, err := p.readNumber(specOctet)
		if err != nil {
			return out, p.fail(start)
		}
		out[i] = byte(v)
	}
	return out, nil
}

type groupRead struct {
	count int
	ipv4  flag
}

func (p *ipParser) separated(index int) bool {
	return index == 0 || p.readChar(':')
}

func (p *ipParser) readEmbeddedIPv4(index int) ([4]byte, error) {
	start := p.pos
	if !p.separated(index) {
		return [4]byte{}, errIPSyntax
	}
	v4, err := p.readIPv4()
	if err != nil {
		return v4, p.fail(start)
	}
	return v4, nil
}

func (p *ipParser) readGroup(index int) (uint16, error) {
	start := p.pos
	if !p.separated(index) {
		return 0, errIPSyntax
	}
	v, err := p.readNumber(specGroup)
	if err != nil {
		return 0, p.fail(start)
	}
	return uint16(v), nil
}

func (p *ipParser) readGroups(groups []uint16) groupRead {
	for i := range groups {
		if i+1 < len(groups) {
			if v4, err := p.readEmbeddedIPv4(i); err == nil {
				groups[i] = uint16(v4[0])<<8 | uint16(v4[1])
				groups[i+1] = uint16(v4[2])<<8 | uint16(v4[3])
				return groupRead{count: i + 2, ipv4: true}
			}
		}
		g, err := p.readGroup(i)
		if err != nil {
			return groupRead{count: i}
		}
		groups[i] = g
	}
	return groupRead{count: len(groups)}
}

func (p *ipParser) readIPv6() ([16]byte, error) {
	start := p.pos
	var head [ipv6Groups]uint16
	var out [16]byte
	h := p.readGroups(head[:])
	if h.count < ipv6Groups {
		if h.ipv4 {
			return out, p.fail(start)
		}
		if !p.readChar(':') {
			return out, p.fail(start)
		}
		if !p.readChar(':') {
			return out, p.fail(start)
		}
		var tail [ipv6Groups - 1]uint16
		t := p.readGroups(tail[:ipv6Groups-(h.count+1)])
		copy(head[ipv6Groups-t.count:], tail[:t.count])
	}
	for i, g := range head {
		out[2*i] = byte(g >> 8)
		out[2*i+1] = byte(g)
	}
	return out, nil
}

func (p *ipParser) readPort() (uint16, error) {
	start := p.pos
	if !p.readChar(':') {
		return 0, errIPSyntax
	}
	v, err := p.readNumber(specPort)
	if err != nil {
		return 0, p.fail(start)
	}
	return uint16(v), nil
}

func (p *ipParser) readSocketV4() (socketAddr, error) {
	start := p.pos
	v4, err := p.readIPv4()
	if err != nil {
		return socketAddr{}, err
	}
	port, err := p.readPort()
	if err != nil {
		return socketAddr{}, p.fail(start)
	}
	s := socketAddr{port: port}
	copy(s.ip.bytes[:4], v4[:])
	return s, nil
}

func (p *ipParser) readScope() uint32 {
	start := p.pos
	if !p.readChar('%') {
		return 0
	}
	v, err := p.readNumber(specScope)
	if err != nil {
		p.pos = start
		return 0
	}
	return uint32(v)
}

func (p *ipParser) readSocketV6() (socketAddr, error) {
	start := p.pos
	if !p.readChar('[') {
		return socketAddr{}, errIPSyntax
	}
	v6, err := p.readIPv6()
	if err != nil {
		return socketAddr{}, p.fail(start)
	}
	scope := p.readScope()
	if !p.readChar(']') {
		return socketAddr{}, p.fail(start)
	}
	port, err := p.readPort()
	if err != nil {
		return socketAddr{}, p.fail(start)
	}
	return socketAddr{ip: ipAddr{v6: true, bytes: v6}, port: port, scope: scope}, nil
}

func parseSocketAddr(in []byte) (socketAddr, error) {
	p := ipParser{in: in}
	s, err := p.readSocketV4()
	if err != nil {
		s, err = p.readSocketV6()
	}
	if err != nil {
		return socketAddr{}, err
	}
	if p.pos != len(in) {
		return socketAddr{}, errIPSyntax
	}
	return s, nil
}

func parseIPAddr(in []byte) (ipAddr, error) {
	p := ipParser{in: in}
	var out ipAddr
	if v4, err := p.readIPv4(); err == nil {
		copy(out.bytes[:4], v4[:])
	} else {
		v6, err := p.readIPv6()
		if err != nil {
			return ipAddr{}, err
		}
		out = ipAddr{v6: true, bytes: v6}
	}
	if p.pos != len(in) {
		return ipAddr{}, errIPSyntax
	}
	return out, nil
}

func (a ipAddr) ipv4Mapped() bool {
	if !a.v6 {
		return false
	}
	for _, b := range a.bytes[:10] {
		if b != 0 {
			return false
		}
	}
	return a.bytes[10] == 0xff && a.bytes[11] == 0xff
}

func appendIPv4(out []byte, b [4]byte) []byte {
	for i := range b {
		if i > 0 {
			out = append(out, '.')
		}
		out = strconv.AppendUint(out, uint64(b[i]), 10)
	}
	return out
}

func appendGroups(out []byte, groups []uint16) []byte {
	for i, g := range groups {
		if i > 0 {
			out = append(out, ':')
		}
		out = strconv.AppendUint(out, uint64(g), 16)
	}
	return out
}

type zeroRun struct {
	start int
	n     int
}

func longestZeroRun(groups [ipv6Groups]uint16) zeroRun {
	best, cur := zeroRun{}, zeroRun{}
	for i, g := range groups {
		if g != 0 {
			cur.n = 0
			continue
		}
		if cur.n == 0 {
			cur.start = i
		}
		cur.n++
		if cur.n > best.n {
			best = cur
		}
	}
	return best
}

func formatIP(a ipAddr) []byte {
	out := make([]byte, 0, ipTextMax)
	if !a.v6 {
		return appendIPv4(out, [4]byte(a.bytes[:4]))
	}
	if a.ipv4Mapped() {
		return appendIPv4(append(out, "::ffff:"...), [4]byte(a.bytes[12:16]))
	}
	var groups [ipv6Groups]uint16
	for i := range groups {
		groups[i] = uint16(a.bytes[2*i])<<8 | uint16(a.bytes[2*i+1])
	}
	run := longestZeroRun(groups)
	if run.n > 1 {
		out = appendGroups(out, groups[:run.start])
		out = append(out, "::"...)
		return appendGroups(out, groups[run.start+run.n:])
	}
	return appendGroups(out, groups[:])
}
