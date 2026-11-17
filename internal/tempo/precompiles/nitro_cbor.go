package precompiles

import (
	"crypto/sha512"
	"unicode/utf8"
)

const (
	nitroMaxDocument    = 24_576
	nitroMaxPayload     = 16_384
	nitroMaxPCRs        = 32
	nitroMaxCABundle    = 32
	nitroMaxCertificate = 1_024
	nitroSignatureSize  = 96
	nitroPCRSlots       = 256
	nitroFieldCount     = 9

	cborUnsigned   = 0x00
	cborNegative   = 0x20
	cborBytes      = 0x40
	cborText       = 0x60
	cborArray      = 0x80
	cborMap        = 0xa0
	cborTag        = 0xc0
	cborNull       = 0xf6
	cborBreak      = 0xff
	cborIndefinite = 31
	coseSign1Tag   = 18
)

const _ = uint(nitroMaxDocument - nitroMaxPayload)
const _ = uint(nitroMaxCABundle*nitroMaxCertificate - nitroMaxPayload)

type nitroPCR struct {
	index int
	value []byte
}

type nitroDoc struct {
	protected     []byte
	payload       []byte
	signature     []byte
	moduleID      []byte
	digest        []byte
	timestamp     uint64
	pcrs          []nitroPCR
	pcrOutOfRange flag
	certificate   []byte
	cabundle      [][]byte
	publicKey     []byte
	hasPublicKey  flag
	userData      []byte
	nonce         []byte
}

type cborDecoder struct {
	data []byte
	pos  int
}

type cborCount struct {
	n          uint64
	indefinite flag
}

func (d *cborDecoder) done() bool { return d.pos == len(d.data) }

func (d *cborDecoder) current() (byte, error) {
	if d.done() {
		return 0, errNitro
	}
	return d.data[d.pos], nil
}

func (d *cborDecoder) read() (byte, error) {
	b, err := d.current()
	if err == nil {
		d.pos++
	}
	return b, err
}

func (d *cborDecoder) slice(n uint64) ([]byte, error) {
	if n > uint64(len(d.data)-d.pos) {
		return nil, errNitro
	}
	start := d.pos
	d.pos += int(n)
	return d.data[start:d.pos], nil
}

func (d *cborDecoder) argument(info byte) (uint64, error) {
	if info < 24 {
		return uint64(info), nil
	}
	if info > 27 {
		return 0, errNitro
	}
	raw, err := d.slice(uint64(1) << (info - 24))
	if err != nil {
		return 0, err
	}
	var v uint64
	for _, b := range raw {
		v = v<<8 | uint64(b)
	}
	return v, nil
}

func (d *cborDecoder) head(major byte) (uint64, error) {
	b, err := d.read()
	if err != nil {
		return 0, err
	}
	if b&0xe0 != major {
		return 0, errNitro
	}
	return d.argument(b & 0x1f)
}

func (d *cborDecoder) uint(limit uint64) (uint64, error) {
	v, err := d.head(cborUnsigned)
	if err != nil {
		return 0, err
	}
	if v > limit {
		return 0, errNitro
	}
	return v, nil
}

func (d *cborDecoder) int8() (int, error) {
	b, err := d.current()
	if err != nil {
		return 0, err
	}
	major := byte(cborUnsigned)
	if b&0xe0 == cborNegative {
		major = cborNegative
	}
	m, err := d.head(major)
	if err != nil {
		return 0, err
	}
	if m > 127 {
		return 0, errNitro
	}
	if major == cborNegative {
		return -1 - int(m), nil
	}
	return int(m), nil
}

func (d *cborDecoder) str(major byte) ([]byte, error) {
	b, err := d.current()
	if err != nil {
		return nil, err
	}
	if b&0x1f == cborIndefinite {
		return nil, errNitro
	}
	n, err := d.head(major)
	if err != nil {
		return nil, err
	}
	return d.slice(n)
}

func (d *cborDecoder) bytes() ([]byte, error) { return d.str(cborBytes) }

func (d *cborDecoder) text() ([]byte, error) {
	v, err := d.str(cborText)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(v) {
		return nil, errNitro
	}
	return v, nil
}

func (d *cborDecoder) collection(major byte) (cborCount, error) {
	b, err := d.current()
	if err != nil {
		return cborCount{}, err
	}
	if b&0xe0 != major {
		return cborCount{}, errNitro
	}
	if b&0x1f == cborIndefinite {
		d.pos++
		return cborCount{indefinite: true}, nil
	}
	n, err := d.head(major)
	return cborCount{n: n}, err
}

func (d *cborDecoder) next(c *cborCount) (bool, error) {
	if !c.indefinite {
		if c.n == 0 {
			return false, nil
		}
		c.n--
		return true, nil
	}
	b, err := d.current()
	if err != nil {
		return false, err
	}
	if b != cborBreak {
		return true, nil
	}
	d.pos++
	return false, nil
}

func parseNitroDocument(document []byte) (nitroDoc, error) {
	if len(document) > nitroMaxDocument {
		return nitroDoc{}, errNitro
	}
	d := cborDecoder{data: document}
	first, err := d.current()
	if err != nil {
		return nitroDoc{}, err
	}
	if first >= 0xc0 && first <= 0xdb {
		tag, err := d.head(cborTag)
		if err != nil {
			return nitroDoc{}, err
		}
		if tag != coseSign1Tag {
			return nitroDoc{}, errNitro
		}
	}
	doc, err := decodeCoseEnvelope(&d)
	if err != nil {
		return nitroDoc{}, err
	}
	if err := validateCoseProtected(doc.protected); err != nil {
		return nitroDoc{}, err
	}
	if len(doc.payload) == 0 || len(doc.payload) > nitroMaxPayload || len(doc.signature) != nitroSignatureSize {
		return nitroDoc{}, errNitro
	}
	if err := decodeNitroPayload(&doc); err != nil {
		return nitroDoc{}, err
	}
	return doc, validateNitroPayload(doc)
}

func decodeCoseEnvelope(d *cborDecoder) (nitroDoc, error) {
	c, err := d.collection(cborArray)
	if err != nil {
		return nitroDoc{}, err
	}
	if c.indefinite || c.n != 4 {
		return nitroDoc{}, errNitro
	}
	var doc nitroDoc
	if doc.protected, err = d.bytes(); err != nil {
		return nitroDoc{}, err
	}
	headers, err := d.collection(cborMap)
	if err != nil {
		return nitroDoc{}, err
	}
	more, err := d.next(&headers)
	if err != nil {
		return nitroDoc{}, err
	}
	if more {
		return nitroDoc{}, errNitro
	}
	if doc.payload, err = d.bytes(); err != nil {
		return nitroDoc{}, err
	}
	if doc.signature, err = d.bytes(); err != nil {
		return nitroDoc{}, err
	}
	if !d.done() {
		return nitroDoc{}, errNitro
	}
	return doc, nil
}

func validateCoseProtected(protected []byte) error {
	d := cborDecoder{data: protected}
	c, err := d.collection(cborMap)
	if err != nil {
		return err
	}
	if c.indefinite || c.n != 1 {
		return errNitro
	}
	key, err := d.uint(0xff)
	if err != nil {
		return err
	}
	alg, err := d.int8()
	if err != nil {
		return err
	}
	if key != 1 || alg != -35 || !d.done() {
		return errNitro
	}
	return nil
}

var nitroFields = [nitroFieldCount]string{"module_id", "digest", "timestamp", "pcrs", "certificate", "cabundle", "public_key", "user_data", "nonce"}

const nitroRequiredFields = 6

func nitroFieldIndex(name []byte) int {
	for i, f := range nitroFields {
		if string(name) == f {
			return i
		}
	}
	return -1
}

func decodeNitroPayload(doc *nitroDoc) error {
	d := cborDecoder{data: doc.payload}
	c, err := d.collection(cborMap)
	if err != nil {
		return err
	}
	var seen [nitroFieldCount]bool
	for range nitroFieldCount + 1 {
		more, err := d.next(&c)
		if err != nil {
			return err
		}
		if !more {
			break
		}
		name, err := d.text()
		if err != nil {
			return err
		}
		i := nitroFieldIndex(name)
		if i < 0 || seen[i] {
			return errNitro
		}
		seen[i] = true
		if err := decodeNitroField(&d, doc, i); err != nil {
			return err
		}
	}
	for _, s := range seen[:nitroRequiredFields] {
		if !s {
			return errNitro
		}
	}
	if !d.done() {
		return errNitro
	}
	return nil
}

func decodeNitroField(d *cborDecoder, doc *nitroDoc, field int) error {
	var err error
	switch nitroFields[field] {
	case "module_id":
		doc.moduleID, err = d.text()
	case "digest":
		doc.digest, err = d.text()
	case "timestamp":
		doc.timestamp, err = d.uint(^uint64(0))
		if err == nil && doc.timestamp == 0 {
			err = errNitro
		}
	case "pcrs":
		err = decodeNitroPCRs(d, doc)
	case "certificate":
		doc.certificate, err = d.bytes()
	case "cabundle":
		doc.cabundle, err = decodeNitroCABundle(d)
	case "public_key":
		var o nitroOptional
		o, err = decodeNitroOptional(d)
		doc.publicKey, doc.hasPublicKey = o.data, o.present
	case "user_data":
		var o nitroOptional
		o, err = decodeNitroOptional(d)
		doc.userData = o.data
	default:
		var o nitroOptional
		o, err = decodeNitroOptional(d)
		doc.nonce = o.data
	}
	return err
}

type nitroOptional struct {
	data    []byte
	present flag
}

func decodeNitroOptional(d *cborDecoder) (nitroOptional, error) {
	b, err := d.current()
	if err != nil {
		return nitroOptional{}, err
	}
	if b == cborNull {
		d.pos++
		return nitroOptional{}, nil
	}
	v, err := d.bytes()
	return nitroOptional{data: v, present: true}, err
}

func decodeNitroPCRs(d *cborDecoder, doc *nitroDoc) error {
	var values [nitroPCRSlots][]byte
	var set [nitroPCRSlots]bool
	c, err := d.collection(cborMap)
	if err != nil {
		return err
	}
	finished := false
	for range nitroPCRSlots + 1 {
		more, err := d.next(&c)
		if err != nil {
			return err
		}
		if !more {
			finished = true
			break
		}
		i, err := d.uint(nitroPCRSlots - 1)
		if err != nil {
			return err
		}
		v, err := d.bytes()
		if err != nil {
			return err
		}
		if set[i] {
			return errNitro
		}
		set[i], values[i] = true, v
	}
	if !finished {
		return errNitro
	}
	doc.pcrs, doc.pcrOutOfRange = nil, false
	for i := range values {
		if !set[i] {
			continue
		}
		if i >= nitroMaxPCRs {
			doc.pcrOutOfRange = true
			continue
		}
		doc.pcrs = append(doc.pcrs, nitroPCR{index: i, value: values[i]})
	}
	return nil
}

func decodeNitroCABundle(d *cborDecoder) ([][]byte, error) {
	c, err := d.collection(cborArray)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for range nitroMaxCABundle + 1 {
		more, err := d.next(&c)
		if err != nil {
			return nil, err
		}
		if !more {
			return out, nil
		}
		cert, err := d.bytes()
		if err != nil {
			return nil, err
		}
		if len(out) == nitroMaxCABundle {
			return nil, errNitro
		}
		out = append(out, cert)
	}
	return nil, errNitro
}

func nitroLenIn(b []byte, hi int) bool { return len(b) >= 1 && len(b) <= hi }

func validateNitroPayload(doc nitroDoc) error {
	if len(doc.moduleID) == 0 || string(doc.digest) != "SHA384" || bool(doc.pcrOutOfRange) || len(doc.pcrs) == 0 {
		return errNitro
	}
	for _, p := range doc.pcrs {
		switch len(p.value) {
		case 32, 48, 64:
		default:
			return errNitro
		}
	}
	if !nitroLenIn(doc.certificate, nitroMaxCertificate) || len(doc.cabundle) == 0 {
		return errNitro
	}
	for _, cert := range doc.cabundle {
		if !nitroLenIn(cert, nitroMaxCertificate) {
			return errNitro
		}
	}
	if bool(doc.hasPublicKey) && !nitroLenIn(doc.publicKey, nitroMaxCertificate) {
		return errNitro
	}
	if len(doc.userData) > 512 || len(doc.nonce) > 512 {
		return errNitro
	}
	return nil
}

func cborHead(major byte, v uint64) []byte {
	if v < 24 {
		return []byte{major | byte(v)}
	}
	width := 8
	switch {
	case v <= 0xff:
		width = 1
	case v <= 0xffff:
		width = 2
	case v <= 0xffff_ffff:
		width = 4
	}
	info := map[int]byte{1: 24, 2: 25, 4: 26, 8: 27}[width]
	out := []byte{major | info}
	for i := width - 1; i >= 0; i-- {
		out = append(out, byte(v>>(8*i)))
	}
	return out
}

func nitroSigStructureHash(doc nitroDoc) [48]byte {
	h := sha512.New384()
	h.Write(cborHead(cborArray, 4))
	h.Write(cborHead(cborText, uint64(len("Signature1"))))
	h.Write([]byte("Signature1"))
	h.Write(cborHead(cborBytes, uint64(len(doc.protected))))
	h.Write(doc.protected)
	h.Write(cborHead(cborBytes, 0))
	h.Write(cborHead(cborBytes, uint64(len(doc.payload))))
	h.Write(doc.payload)
	var out [48]byte
	copy(out[:], h.Sum(nil))
	return out
}
