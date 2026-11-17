package precompiles

import "errors"

var errNitro = errors.New("nitro: invalid attestation")

const (
	derBoolean     = 0x01
	derInteger     = 0x02
	derBitString   = 0x03
	derOctetString = 0x04
	derOID         = 0x06
	derUTCTime     = 0x17
	derGenTime     = 0x18
	derSequence    = 0x30
	derSet         = 0x31

	derLengthMax   = 0x0fff_ffff
	derOIDBytesMax = 39
	derYearMin     = 1970
	derUnixMax     = 253_402_300_799
)

const _ = uint(1<<31 - 1 - derLengthMax)
const _ = uint(nitroMaxCertificate - derOIDBytesMax)
const _ = uint(derUnixMax - derYearMin)

type derElement struct {
	tag   byte
	tlv   []byte
	value []byte
}

type derReader struct {
	data []byte
	pos  int
}

type derBits struct {
	unused byte
	data   []byte
}

type derCivil struct {
	year   int
	month  int
	day    int
	hour   int
	minute int
	second int
}

func derTagValid(b byte) bool {
	if b&0x1f == 0x1f {
		return false
	}
	switch {
	case b >= 0x01 && b <= 0x06, b == 0x09, b == 0x0a, b == 0x0c:
		return true
	case b >= 0x12 && b <= 0x18, b == 0x1a, b == 0x1e:
		return true
	case b == derSequence, b == derSet:
		return true
	case b >= 0x40 && b <= 0x7e, b >= 0x80 && b <= 0xbe, b >= 0xc0 && b <= 0xfe:
		return true
	}
	return false
}

func (r *derReader) done() bool { return r.pos == len(r.data) }

func (r *derReader) peek() (byte, bool) {
	if r.done() {
		return 0, false
	}
	return r.data[r.pos], true
}

func (r *derReader) peekIs(tag byte) bool {
	b, ok := r.peek()
	return ok && b == tag
}

func (r *derReader) byte() (byte, error) {
	if r.done() {
		return 0, errNitro
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func derLengthOctet(n int) byte {
	switch {
	case n < 0x80:
		return byte(n)
	case n <= 0xff:
		return 0x81
	case n <= 0xffff:
		return 0x82
	case n <= 0xff_ffff:
		return 0x83
	}
	return 0x84
}

func derLengthEncoding(n int) []byte {
	first := derLengthOctet(n)
	if first < 0x80 {
		return []byte{first}
	}
	count := int(first - 0x80)
	out := []byte{first}
	for i := count - 1; i >= 0; i-- {
		out = append(out, byte(n>>(8*i)))
	}
	return out
}

func (r *derReader) length() (int, error) {
	first, err := r.byte()
	if err != nil {
		return 0, err
	}
	if first < 0x80 {
		return int(first), nil
	}
	if first == 0x80 || first > 0x84 {
		return 0, errNitro
	}
	n := 0
	for range int(first - 0x80) {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		n = n<<8 | int(b)
	}
	if n > derLengthMax || derLengthOctet(n) != first {
		return 0, errNitro
	}
	return n, nil
}

func (r *derReader) any() (derElement, error) {
	start := r.pos
	tag, err := r.byte()
	if err != nil {
		return derElement{}, err
	}
	if !derTagValid(tag) {
		return derElement{}, errNitro
	}
	n, err := r.length()
	if err != nil {
		return derElement{}, err
	}
	if n > len(r.data)-r.pos {
		return derElement{}, errNitro
	}
	body := r.pos
	r.pos += n
	return derElement{tag: tag, tlv: r.data[start:r.pos], value: r.data[body:r.pos]}, nil
}

func (r *derReader) expect(tag byte) (derElement, error) {
	e, err := r.any()
	if err != nil {
		return derElement{}, err
	}
	if e.tag != tag {
		return derElement{}, errNitro
	}
	return e, nil
}

func derOnly(data []byte, tag byte) (derElement, error) {
	r := derReader{data: data}
	e, err := r.expect(tag)
	if err != nil {
		return derElement{}, err
	}
	if !r.done() {
		return derElement{}, errNitro
	}
	return e, nil
}

func derOIDValid(v []byte) bool {
	if len(v) < 3 || len(v) > derOIDBytesMax || v[0] >= 120 {
		return false
	}
	arc := 0
	for _, b := range v[1:] {
		arc++
		if arc > 4 && b&0xf0 != 0 {
			return false
		}
		if b&0x80 == 0 {
			arc = 0
		}
	}
	return arc == 0
}

func derUintMagnitude(v []byte) ([]byte, error) {
	if len(v) == 0 || v[0] >= 0x80 {
		return nil, errNitro
	}
	if v[0] != 0 || len(v) == 1 {
		return v, nil
	}
	if v[1] < 0x80 {
		return nil, errNitro
	}
	return v[1:], nil
}

func derIntCanonical(v []byte) bool {
	switch {
	case len(v) == 0:
		return false
	case len(v) == 1:
		return true
	case v[0] == 0x00 && v[1] < 0x80:
		return false
	case v[0] == 0xff && v[1] >= 0x80:
		return false
	}
	return true
}

func derUint8(v []byte) (byte, error) {
	if len(v) > 2 {
		return 0, errNitro
	}
	m, err := derUintMagnitude(v)
	if err != nil {
		return 0, err
	}
	if len(m) != 1 {
		return 0, errNitro
	}
	return m[0], nil
}

func derBool(v []byte) (bool, error) {
	if len(v) != 1 {
		return false, errNitro
	}
	switch v[0] {
	case 0x00:
		return false, nil
	case 0xff:
		return true, nil
	}
	return false, errNitro
}

func derBitStringOf(v []byte) (derBits, error) {
	if len(v) == 0 || v[0] > 7 {
		return derBits{}, errNitro
	}
	if v[0] != 0 && len(v) == 1 {
		return derBits{}, errNitro
	}
	return derBits{unused: v[0], data: v[1:]}, nil
}

func (b derBits) count() int { return len(b.data)*8 - int(b.unused) }

func (b derBits) bit(i int) bool {
	if i >= b.count() {
		return false
	}
	return b.data[i/8]>>(7-i%8)&1 == 1
}

func derPair(p []byte) (int, error) {
	if p[0] < '0' || p[0] > '9' {
		return 0, errNitro
	}
	if p[1] < '0' || p[1] > '9' {
		return 0, errNitro
	}
	return int(p[0]-'0')*10 + int(p[1]-'0'), nil
}

func derPairs(digits []byte) ([]int, error) {
	out := make([]int, len(digits)/2)
	for i := range out {
		v, err := derPair(digits[2*i : 2*i+2])
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func derTime(e derElement) (uint64, error) {
	v := e.value
	var digits []byte
	switch {
	case e.tag == derUTCTime && len(v) == 13:
		digits = v[:12]
	case e.tag == derGenTime && len(v) == 15:
		digits = v[:14]
	default:
		return 0, errNitro
	}
	if v[len(v)-1] != 'Z' {
		return 0, errNitro
	}
	p, err := derPairs(digits)
	if err != nil {
		return 0, err
	}
	year := p[0]*100 + p[1]
	if e.tag == derUTCTime {
		year = 2000 + p[0]
		if p[0] >= 50 {
			year = 1900 + p[0]
		}
	}
	rest := p[len(p)-5:]
	return derSeconds(derCivil{year: year, month: rest[0], day: rest[1], hour: rest[2], minute: rest[3], second: rest[4]})
}

func derLeap(year int) bool {
	if year%400 == 0 {
		return true
	}
	return year%4 == 0 && year%100 != 0
}

func derCivilValid(c derCivil) bool {
	if c.year < derYearMin || c.month < 1 || c.month > 12 || c.day < 1 || c.day > 31 {
		return false
	}
	return c.hour <= 23 && c.minute <= 59 && c.second <= 59
}

func derSeconds(c derCivil) (uint64, error) {
	if !derCivilValid(c) {
		return 0, errNitro
	}
	before := [12]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	lengths := [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	leap := derLeap(c.year)
	days := lengths[c.month-1]
	if c.month == 2 && leap {
		days = 29
	}
	if c.day > days {
		return 0, errNitro
	}
	yearDays := before[c.month-1] + c.day - 1
	if leap && c.month > 2 {
		yearDays++
	}
	y := c.year - 1
	leaps := (y-1968)/4 - (y-1900)/100 + (y-1600)/400
	total := uint64((c.year-1970)*365+leaps+yearDays)*86_400 + uint64(c.hour*3_600+c.minute*60+c.second)
	if total > derUnixMax {
		return 0, errNitro
	}
	return total, nil
}
