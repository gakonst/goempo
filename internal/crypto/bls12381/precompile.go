package bls12381

import "errors"

const (
	G1Length        = 2 * EncodedFpLength
	G2Length        = 4 * EncodedFpLength
	ScalarLength    = 32
	G1MSMPairLength = G1Length + ScalarLength
	G2MSMPairLength = G2Length + ScalarLength
	PairingLength   = G1Length + G2Length
	OutputWord      = 32
	G1AddGas        = 375
	G2AddGas        = 600
	G1MulGas        = 12000
	G2MulGas        = 22500
	PairingBaseGas  = 37700
	PairingPairGas  = 32600
	MapG1Gas        = 5500
	MapG2Gas        = 23800
	discountScale   = 1000
	discountEntries = 128
)

const _ = uint(G2MulGas - G1MulGas)

const _ = uint(G2AddGas - G1AddGas)

const _ = uint(MapG2Gas - MapG1Gas)

const _ = uint(PairingBaseGas - PairingPairGas)

var (
	ErrInputLength   = errors.New("bls12381: invalid input length")
	ErrInvalidPoint  = errors.New("bls12381: invalid point encoding or point not on curve")
	ErrNotInSubgroup = errors.New("bls12381: point not in the prime-order subgroup")
	ErrInvalidField  = errors.New("bls12381: invalid field element")
)

var g1Discount = [discountEntries]uint16{1000, 949, 848, 797, 764, 750, 738, 728, 719, 712, 705, 698, 692, 687, 682, 677, 673, 669, 665, 661, 658, 654, 651, 648, 645, 642, 640, 637, 635, 632, 630, 627, 625, 623, 621, 619, 617, 615, 613, 611, 609, 608, 606, 604, 603, 601, 599, 598, 596, 595, 593, 592, 591, 589, 588, 586, 585, 584, 582, 581, 580, 579, 577, 576, 575, 574, 573, 572, 570, 569, 568, 567, 566, 565, 564, 563, 562, 561, 560, 559, 558, 557, 556, 555, 554, 553, 552, 551, 550, 549, 548, 547, 547, 546, 545, 544, 543, 542, 541, 540, 540, 539, 538, 537, 536, 536, 535, 534, 533, 532, 532, 531, 530, 529, 528, 528, 527, 526, 525, 525, 524, 523, 522, 522, 521, 520, 520, 519}

var g2Discount = [discountEntries]uint16{1000, 1000, 923, 884, 855, 832, 812, 796, 782, 770, 759, 749, 740, 732, 724, 717, 711, 704, 699, 693, 688, 683, 679, 674, 670, 666, 663, 659, 655, 652, 649, 646, 643, 640, 637, 634, 632, 629, 627, 624, 622, 620, 618, 615, 613, 611, 609, 607, 606, 604, 602, 600, 598, 597, 595, 593, 592, 590, 589, 587, 586, 584, 583, 582, 580, 579, 578, 576, 575, 574, 573, 571, 570, 569, 568, 567, 566, 565, 563, 562, 561, 560, 559, 558, 557, 556, 555, 554, 553, 552, 552, 551, 550, 549, 548, 547, 546, 545, 545, 544, 543, 542, 541, 541, 540, 539, 538, 537, 537, 536, 535, 535, 534, 533, 532, 532, 531, 530, 530, 529, 528, 528, 527, 526, 526, 525, 524, 524}

type msmPricing struct {
	pairLength int
	mulGas     uint64
	discount   *[discountEntries]uint16
}

func (m msmPricing) gas(inputLength int) uint64 {
	k := uint64(inputLength / m.pairLength)
	if k == 0 {
		return 0
	}
	d := uint64(m.discount[min(k, discountEntries)-1])
	return k * m.mulGas * d / discountScale
}

func G1MSMGas(inputLength int) uint64 {
	return msmPricing{pairLength: G1MSMPairLength, mulGas: G1MulGas, discount: &g1Discount}.gas(inputLength)
}

func G2MSMGas(inputLength int) uint64 {
	return msmPricing{pairLength: G2MSMPairLength, mulGas: G2MulGas, discount: &g2Discount}.gas(inputLength)
}

func PairingGas(inputLength int) uint64 {
	return PairingBaseGas + PairingPairGas*uint64(inputLength/PairingLength)
}

func G1Add(input []byte) ([G1Length]byte, error) {
	var out [G1Length]byte
	if len(input) != 2*G1Length {
		return out, ErrInputLength
	}
	a, ok := decodeG1(input[:G1Length])
	if !ok {
		return out, ErrInvalidPoint
	}
	b, ok := decodeG1(input[G1Length:])
	if !ok {
		return out, ErrInvalidPoint
	}
	a.jac().add(b.jac()).affine().encode(out[:])
	return out, nil
}

func G2Add(input []byte) ([G2Length]byte, error) {
	var out [G2Length]byte
	if len(input) != 2*G2Length {
		return out, ErrInputLength
	}
	a, ok := decodeG2(input[:G2Length])
	if !ok {
		return out, ErrInvalidPoint
	}
	b, ok := decodeG2(input[G2Length:])
	if !ok {
		return out, ErrInvalidPoint
	}
	a.jac().add(b.jac()).affine().encode(out[:])
	return out, nil
}

func G1MSM(input []byte) ([G1Length]byte, error) {
	var out [G1Length]byte
	if len(input) == 0 || len(input)%G1MSMPairLength != 0 {
		return out, ErrInputLength
	}
	n := len(input) / G1MSMPairLength
	points := make([]g1Jac, 0, n)
	scalars := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		chunk := input[i*G1MSMPairLength : (i+1)*G1MSMPairLength]
		p, ok := decodeG1(chunk[:G1Length])
		if !ok {
			return out, ErrInvalidPoint
		}
		if !p.inSubgroup() {
			return out, ErrNotInSubgroup
		}
		points = append(points, p.jac())
		scalars = append(scalars, chunk[G1Length:])
	}
	msmG1(points, scalars).affine().encode(out[:])
	return out, nil
}

func G2MSM(input []byte) ([G2Length]byte, error) {
	var out [G2Length]byte
	if len(input) == 0 || len(input)%G2MSMPairLength != 0 {
		return out, ErrInputLength
	}
	n := len(input) / G2MSMPairLength
	points := make([]g2Jac, 0, n)
	scalars := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		chunk := input[i*G2MSMPairLength : (i+1)*G2MSMPairLength]
		p, ok := decodeG2(chunk[:G2Length])
		if !ok {
			return out, ErrInvalidPoint
		}
		if !p.inSubgroup() {
			return out, ErrNotInSubgroup
		}
		points = append(points, p.jac())
		scalars = append(scalars, chunk[G2Length:])
	}
	msmG2(points, scalars).affine().encode(out[:])
	return out, nil
}

func PairingCheck(input []byte) ([OutputWord]byte, error) {
	var out [OutputWord]byte
	if len(input) == 0 || len(input)%PairingLength != 0 {
		return out, ErrInputLength
	}
	n := len(input) / PairingLength
	pairs := make([]pair, 0, n)
	for i := 0; i < n; i++ {
		chunk := input[i*PairingLength : (i+1)*PairingLength]
		p, ok := decodeG1(chunk[:G1Length])
		if !ok {
			return out, ErrInvalidPoint
		}
		q, ok := decodeG2(chunk[G1Length:])
		if !ok {
			return out, ErrInvalidPoint
		}
		if !p.inSubgroup() || !q.inSubgroup() {
			return out, ErrNotInSubgroup
		}
		pairs = append(pairs, pair{p: p, q: q})
	}
	if pairingProduct(pairs).isOne() {
		out[OutputWord-1] = 1
	}
	return out, nil
}

func MapFpToG1(input []byte) ([G1Length]byte, error) {
	var out [G1Length]byte
	if len(input) != EncodedFpLength {
		return out, ErrInputLength
	}
	u, ok := decodeFp(input)
	if !ok {
		return out, ErrInvalidField
	}
	mapToG1(u).encode(out[:])
	return out, nil
}

func MapFp2ToG2(input []byte) ([G2Length]byte, error) {
	var out [G2Length]byte
	if len(input) != 2*EncodedFpLength {
		return out, ErrInputLength
	}
	u, ok := decodeFp2(input)
	if !ok {
		return out, ErrInvalidField
	}
	mapToG2(u).encode(out[:])
	return out, nil
}
