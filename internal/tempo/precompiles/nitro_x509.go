package precompiles

import (
	"bytes"
	"crypto/ecdsa"
)

const (
	x509SerialMax     = 21
	x509VersionV3     = 2
	x509ExtensionsMax = nitroMaxCertificate/9 + 1
	x509KeyUsageMax   = 16
	x509DigitalSig    = 0
	x509KeyCertSign   = 5
)

const _ = uint(nitroMaxCertificate - x509SerialMax)
const _ = uint(nitroMaxCertificate - x509ExtensionsMax)
const _ = uint(x509KeyUsageMax - x509KeyCertSign - 1)

var (
	oidECDSAWithSHA384 = []byte{0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x03}
	oidECPublicKey     = []byte{0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01}
	oidSecp384r1       = []byte{0x2b, 0x81, 0x04, 0x00, 0x22}
	oidBasicConstraint = []byte{0x55, 0x1d, 0x13}
	oidKeyUsage        = []byte{0x55, 0x1d, 0x0f}
)

type x509Role uint8

const (
	x509CA x509Role = iota
	x509Leaf
)

type x509Algorithm struct {
	oid       []byte
	params    derElement
	hasParams flag
}

type x509Extension struct {
	id       []byte
	critical flag
	value    []byte
}

type x509Cert struct {
	tbs        []byte
	version    byte
	sigInner   x509Algorithm
	sigOuter   x509Algorithm
	issuer     []byte
	subject    []byte
	notBefore  uint64
	notAfter   uint64
	keyAlg     x509Algorithm
	key        derBits
	extensions []x509Extension
	signature  derBits
}

type x509Checked struct {
	key       *ecdsa.PublicKey
	tbs       []byte
	signature []byte
	issuer    []byte
	subject   []byte
	pathLen   int
}

type x509Basic struct {
	ca      flag
	pathLen int
}

type x509Attribute struct {
	length int
	oid    []byte
	value  derElement
}

type x509CheckInput struct {
	der  []byte
	role x509Role
	now  uint64
}

func nitroValidateChain(doc nitroDoc, now uint64) (*ecdsa.PublicKey, error) {
	if !bytes.Equal(doc.cabundle[0], awsNitroRootDER) {
		return nil, errNitro
	}
	chain := make([]x509Checked, 0, len(doc.cabundle)+1)
	for _, cert := range doc.cabundle {
		c, err := x509Check(x509CheckInput{der: cert, role: x509CA, now: now})
		if err != nil {
			return nil, err
		}
		chain = append(chain, c)
	}
	leaf, err := x509Check(x509CheckInput{der: doc.certificate, role: x509Leaf, now: now})
	if err != nil {
		return nil, err
	}
	chain = append(chain, leaf)
	for i := 1; i < len(chain); i++ {
		if !bytes.Equal(chain[i].issuer, chain[i-1].subject) {
			return nil, errNitro
		}
	}
	if err := x509PathLengths(chain[:len(doc.cabundle)]); err != nil {
		return nil, err
	}
	for i := 1; i < len(chain); i++ {
		sig, err := p384SignatureFromDER(chain[i].signature)
		if err != nil || !p384VerifyFixed(p384Check{key: chain[i-1].key, digest: sha384Of(chain[i].tbs), sig: sig}) {
			return nil, errNitro
		}
	}
	return leaf.key, nil
}

func x509PathLengths(authorities []x509Checked) error {
	for i, a := range authorities {
		if a.pathLen < 0 {
			continue
		}
		below := 0
		for _, c := range authorities[i+1:] {
			if !bytes.Equal(c.subject, c.issuer) {
				below++
			}
		}
		if below > a.pathLen {
			return errNitro
		}
	}
	return nil
}

func x509Check(in x509CheckInput) (x509Checked, error) {
	cert, err := x509Parse(in.der)
	if err != nil {
		return x509Checked{}, err
	}
	if cert.version != x509VersionV3 {
		return x509Checked{}, errNitro
	}
	if !x509SigAlgOK(cert.sigOuter) || !x509SigAlgOK(cert.sigInner) {
		return x509Checked{}, errNitro
	}
	if in.now < cert.notBefore || in.now > cert.notAfter {
		return x509Checked{}, errNitro
	}
	pathLen, err := x509Extensions(cert, in.role)
	if err != nil {
		return x509Checked{}, err
	}
	key, err := x509PublicKey(cert)
	if err != nil {
		return x509Checked{}, err
	}
	if cert.signature.unused != 0 {
		return x509Checked{}, errNitro
	}
	if _, err := p384SignatureFromDER(cert.signature.data); err != nil {
		return x509Checked{}, err
	}
	return x509Checked{key: key, tbs: cert.tbs, signature: cert.signature.data, issuer: cert.issuer, subject: cert.subject, pathLen: pathLen}, nil
}

func x509SigAlgOK(a x509Algorithm) bool {
	return bytes.Equal(a.oid, oidECDSAWithSHA384) && !bool(a.hasParams)
}

func x509FindExtension(exts []x509Extension, id []byte) (x509Extension, bool) {
	for _, e := range exts {
		if bytes.Equal(e.id, id) {
			return e, true
		}
	}
	return x509Extension{}, false
}

func x509ExtensionSetValid(exts []x509Extension) bool {
	for i, e := range exts {
		if _, dup := x509FindExtension(exts[:i], e.id); dup {
			return false
		}
		known := bytes.Equal(e.id, oidBasicConstraint) || bytes.Equal(e.id, oidKeyUsage)
		if bool(e.critical) && !known {
			return false
		}
	}
	return true
}

func x509Extensions(cert x509Cert, role x509Role) (int, error) {
	if !x509ExtensionSetValid(cert.extensions) {
		return 0, errNitro
	}
	basicExt, hasBasic := x509FindExtension(cert.extensions, oidBasicConstraint)
	basic := x509Basic{pathLen: -1}
	if hasBasic {
		b, err := x509DecodeBasic(basicExt.value)
		if err != nil {
			return 0, err
		}
		basic = b
	}
	usageExt, hasUsage := x509FindExtension(cert.extensions, oidKeyUsage)
	if !hasUsage {
		return 0, errNitro
	}
	usage, err := x509DecodeKeyUsage(usageExt.value)
	if err != nil {
		return 0, err
	}
	if role == x509CA {
		if !hasBasic || !bool(basicExt.critical) || !bool(basic.ca) || !usage.bit(x509KeyCertSign) {
			return 0, errNitro
		}
		return basic.pathLen, nil
	}
	if bool(basic.ca) || basic.pathLen >= 0 || !usage.bit(x509DigitalSig) {
		return 0, errNitro
	}
	return -1, nil
}

func x509DecodeBasic(v []byte) (x509Basic, error) {
	seq, err := derOnly(v, derSequence)
	if err != nil {
		return x509Basic{}, err
	}
	fields := derReader{data: seq.value}
	out := x509Basic{pathLen: -1}
	if err := x509PeekValid(&fields); err != nil {
		return x509Basic{}, err
	}
	if fields.peekIs(derBoolean) {
		e, err := fields.any()
		if err != nil {
			return x509Basic{}, err
		}
		ca, err := derBool(e.value)
		if err != nil {
			return x509Basic{}, err
		}
		out.ca = flag(ca)
	}
	if err := x509PeekValid(&fields); err != nil {
		return x509Basic{}, err
	}
	if fields.peekIs(derInteger) {
		e, err := fields.any()
		if err != nil {
			return x509Basic{}, err
		}
		n, err := derUint8(e.value)
		if err != nil {
			return x509Basic{}, err
		}
		out.pathLen = int(n)
	}
	if !fields.done() {
		return x509Basic{}, errNitro
	}
	return out, nil
}

func x509PeekValid(r *derReader) error {
	b, ok := r.peek()
	if ok && !derTagValid(b) {
		return errNitro
	}
	return nil
}

func x509DecodeKeyUsage(v []byte) (derBits, error) {
	e, err := derOnly(v, derBitString)
	if err != nil {
		return derBits{}, err
	}
	bits, err := derBitStringOf(e.value)
	if err != nil {
		return derBits{}, err
	}
	if bits.count() > x509KeyUsageMax {
		return derBits{}, errNitro
	}
	return bits, nil
}

func x509PublicKey(cert x509Cert) (*ecdsa.PublicKey, error) {
	a := cert.keyAlg
	if !bytes.Equal(a.oid, oidECPublicKey) || !bool(a.hasParams) || a.params.tag != derOID {
		return nil, errNitro
	}
	if !bytes.Equal(a.params.value, oidSecp384r1) || cert.key.unused != 0 {
		return nil, errNitro
	}
	return p384Key(cert.key.data)
}

func x509Parse(raw []byte) (x509Cert, error) {
	seq, err := derOnly(raw, derSequence)
	if err != nil {
		return x509Cert{}, err
	}
	fields := derReader{data: seq.value}
	tbs, err := fields.expect(derSequence)
	if err != nil {
		return x509Cert{}, err
	}
	cert := x509Cert{tbs: tbs.tlv}
	if cert.sigOuter, err = x509ParseAlgorithm(&fields); err != nil {
		return x509Cert{}, err
	}
	sig, err := fields.expect(derBitString)
	if err != nil {
		return x509Cert{}, err
	}
	if cert.signature, err = derBitStringOf(sig.value); err != nil {
		return x509Cert{}, err
	}
	if !fields.done() {
		return x509Cert{}, errNitro
	}
	return cert, x509ParseTBS(tbs.value, &cert)
}

func x509ParseTBS(v []byte, cert *x509Cert) error {
	fields := derReader{data: v}
	var err error
	if cert.version, err = x509ParseVersion(&fields); err != nil {
		return err
	}
	serial, err := fields.expect(derInteger)
	if err != nil {
		return err
	}
	if !derIntCanonical(serial.value) || len(serial.value) > x509SerialMax {
		return errNitro
	}
	if cert.sigInner, err = x509ParseAlgorithm(&fields); err != nil {
		return err
	}
	if cert.issuer, err = x509ParseName(&fields); err != nil {
		return err
	}
	if err := x509ParseValidity(&fields, cert); err != nil {
		return err
	}
	if cert.subject, err = x509ParseName(&fields); err != nil {
		return err
	}
	if err := x509ParseKeyInfo(&fields, cert); err != nil {
		return err
	}
	return x509ParseTBSTail(&fields, cert)
}

func x509ParseKeyInfo(fields *derReader, cert *x509Cert) error {
	info, err := fields.expect(derSequence)
	if err != nil {
		return err
	}
	kf := derReader{data: info.value}
	if cert.keyAlg, err = x509ParseAlgorithm(&kf); err != nil {
		return err
	}
	key, err := kf.expect(derBitString)
	if err != nil {
		return err
	}
	if cert.key, err = derBitStringOf(key.value); err != nil {
		return err
	}
	if !kf.done() {
		return errNitro
	}
	return nil
}

func x509ParseTBSTail(fields *derReader, cert *x509Cert) error {
	for _, tag := range [2]byte{0x81, 0x82} {
		if !fields.peekIs(tag) {
			continue
		}
		e, err := fields.any()
		if err != nil {
			return err
		}
		if _, err := derBitStringOf(e.value); err != nil {
			return err
		}
	}
	if fields.peekIs(0xa3) {
		e, err := fields.any()
		if err != nil {
			return err
		}
		if cert.extensions, err = x509ParseExtensions(e.value); err != nil {
			return err
		}
	}
	if !fields.done() {
		return errNitro
	}
	return nil
}

func x509ParseVersion(fields *derReader) (byte, error) {
	if !fields.peekIs(0xa0) {
		return 0, nil
	}
	e, err := fields.any()
	if err != nil {
		return 0, err
	}
	inner, err := derOnly(e.value, derInteger)
	if err != nil {
		return 0, err
	}
	v, err := derUint8(inner.value)
	if err != nil {
		return 0, err
	}
	if v > x509VersionV3 || v == 0 {
		return 0, errNitro
	}
	return v, nil
}

func x509ParseAlgorithm(fields *derReader) (x509Algorithm, error) {
	seq, err := fields.expect(derSequence)
	if err != nil {
		return x509Algorithm{}, err
	}
	af := derReader{data: seq.value}
	oid, err := af.expect(derOID)
	if err != nil {
		return x509Algorithm{}, err
	}
	if !derOIDValid(oid.value) {
		return x509Algorithm{}, errNitro
	}
	out := x509Algorithm{oid: oid.value}
	if !af.done() {
		if out.params, err = af.any(); err != nil {
			return x509Algorithm{}, err
		}
		out.hasParams = true
	}
	if !af.done() {
		return x509Algorithm{}, errNitro
	}
	return out, nil
}

func x509ParseValidity(fields *derReader, cert *x509Cert) error {
	seq, err := fields.expect(derSequence)
	if err != nil {
		return err
	}
	times := derReader{data: seq.value}
	var out [2]uint64
	for i := range out {
		e, err := times.any()
		if err != nil {
			return err
		}
		if out[i], err = derTime(e); err != nil {
			return err
		}
	}
	if !times.done() {
		return errNitro
	}
	cert.notBefore, cert.notAfter = out[0], out[1]
	return nil
}

func x509ParseName(fields *derReader) ([]byte, error) {
	name, err := fields.expect(derSequence)
	if err != nil {
		return nil, err
	}
	names := derReader{data: name.value}
	for range len(name.value)/2 + 1 {
		if names.done() {
			return name.tlv, nil
		}
		set, err := names.expect(derSet)
		if err != nil {
			return nil, err
		}
		if err := x509ParseRDN(set.value); err != nil {
			return nil, err
		}
	}
	return nil, errFatal
}

func x509ParseRDN(v []byte) error {
	attrs := derReader{data: v}
	var prev *x509Attribute
	for range len(v)/2 + 1 {
		if attrs.done() {
			return nil
		}
		e, err := attrs.expect(derSequence)
		if err != nil {
			return err
		}
		parts := derReader{data: e.value}
		oid, err := parts.expect(derOID)
		if err != nil {
			return err
		}
		if !derOIDValid(oid.value) {
			return errNitro
		}
		val, err := parts.any()
		if err != nil {
			return err
		}
		if !parts.done() {
			return errNitro
		}
		a := x509Attribute{length: len(e.value), oid: oid.value, value: val}
		if prev != nil && prev.compare(a) >= 0 {
			return errNitro
		}
		prev = &a
	}
	return errFatal
}

func (a x509Attribute) compare(b x509Attribute) int {
	if c := bytes.Compare(derLengthEncoding(a.length), derLengthEncoding(b.length)); c != 0 {
		return c
	}
	if c := bytes.Compare(derLengthEncoding(len(a.oid)), derLengthEncoding(len(b.oid))); c != 0 {
		return c
	}
	if c := bytes.Compare(a.oid, b.oid); c != 0 {
		return c
	}
	if a.value.tag != b.value.tag {
		if a.value.tag < b.value.tag {
			return -1
		}
		return 1
	}
	if c := bytes.Compare(derLengthEncoding(len(a.value.value)), derLengthEncoding(len(b.value.value))); c != 0 {
		return c
	}
	return bytes.Compare(a.value.value, b.value.value)
}

func x509ParseExtensions(v []byte) ([]x509Extension, error) {
	list, err := derOnly(v, derSequence)
	if err != nil {
		return nil, err
	}
	entries := derReader{data: list.value}
	var out []x509Extension
	for range x509ExtensionsMax + 1 {
		if entries.done() {
			return out, nil
		}
		ext, err := x509ParseExtension(&entries)
		if err != nil {
			return nil, err
		}
		out = append(out, ext)
	}
	return nil, errFatal
}

func x509ParseExtension(entries *derReader) (x509Extension, error) {
	seq, err := entries.expect(derSequence)
	if err != nil {
		return x509Extension{}, err
	}
	fields := derReader{data: seq.value}
	id, err := fields.expect(derOID)
	if err != nil {
		return x509Extension{}, err
	}
	if !derOIDValid(id.value) {
		return x509Extension{}, errNitro
	}
	ext := x509Extension{id: id.value}
	if fields.peekIs(derBoolean) {
		e, err := fields.any()
		if err != nil {
			return x509Extension{}, err
		}
		critical, err := derBool(e.value)
		if err != nil {
			return x509Extension{}, err
		}
		if !critical {
			return x509Extension{}, errNitro
		}
		ext.critical = true
	}
	val, err := fields.expect(derOctetString)
	if err != nil {
		return x509Extension{}, err
	}
	if !fields.done() {
		return x509Extension{}, errNitro
	}
	ext.value = val.value
	return ext, nil
}
