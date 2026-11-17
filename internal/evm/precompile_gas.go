package evm

import "math/bits"

const (
	modexpLengthWord  = 32
	modexpOsakaLimit  = 1024
	blake2fInputSize  = 213
	bnPairSize        = 192
	blsG1AddInput     = 256
	blsG2AddInput     = 512
	blsG1MulPair      = 160
	blsG2MulPair      = 288
	blsPairingPair    = 384
	blsMapFpInput     = 64
	blsMapFp2Input    = 128
	pointEvalInput    = 192
	pointEvalGas      = 50000
	blsDiscountsCount = 128
)

const _ = uint(blsG2MulPair - blsG1MulPair)

type sat uint64

func (a sat) mul(b sat) sat {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi != 0 {
		return sat(^uint64(0))
	}
	return sat(lo)
}

func (a sat) add(b sat) sat {
	sum, carry := bits.Add64(uint64(a), uint64(b), 0)
	if carry != 0 {
		return sat(^uint64(0))
	}
	return sat(sum)
}

func paddedSlice(input []byte, span memRange) []byte {
	out := make([]byte, span.size)
	if span.offset < uint64(len(input)) {
		copy(out, input[span.offset:])
	}
	return out
}

func lengthWord(input []byte, index uint64) uint64 {
	word := paddedSlice(input, memRange{offset: index * modexpLengthWord, size: modexpLengthWord})
	for i := 0; i < modexpLengthWord-8; i++ {
		if word[i] != 0 {
			return ^uint64(0)
		}
	}
	v := uint64(0)
	for i := modexpLengthWord - 8; i < modexpLengthWord; i++ {
		v = v<<8 | uint64(word[i])
	}
	return v
}

type modexpHeader struct {
	baseLen uint64
	expLen  uint64
	modLen  uint64
}

func readModexpHeader(input []byte) modexpHeader {
	return modexpHeader{baseLen: lengthWord(input, 0), expLen: lengthWord(input, 1), modLen: lengthWord(input, 2)}
}

func (h modexpHeader) expHead(input []byte) []byte {
	offset := uint64(sat(3 * modexpLengthWord).add(sat(h.baseLen)))
	return paddedSlice(input, memRange{offset: offset, size: min(h.expLen, modexpLengthWord)})
}

func bitLen(b []byte) uint64 {
	for i := 0; i < len(b); i++ {
		if b[i] != 0 {
			return uint64(len(b)-i-1)*8 + uint64(bits.Len8(b[i]))
		}
	}
	return 0
}

type modexpPricing struct {
	osaka  bool
	eip198 bool
}

func eip198Complexity(x sat) sat {
	if x <= 64 {
		return x.mul(x)
	}
	if x <= 1024 {
		return x.mul(x)/4 + x.mul(96) - 3072
	}
	return x.mul(x)/16 + x.mul(480) - 199680
}

func (h modexpHeader) oversized() bool {
	return h.baseLen > modexpOsakaLimit || h.expLen > modexpOsakaLimit || h.modLen > modexpOsakaLimit
}

func (p modexpPricing) gas(input []byte) uint64 {
	h := readModexpHeader(input)
	if p.osaka {
		if h.oversized() {
			return ^uint64(0)
		}
	}
	maxLen := max(h.baseLen, h.modLen)
	words := sat(maxLen).add(7) / 8
	complexity := words.mul(words)
	if p.osaka {
		complexity = 16
		if maxLen > modexpLengthWord {
			complexity = sat(2).mul(words).mul(words)
		}
	}
	headBits := bitLen(h.expHead(input))
	iterations := sat(0)
	multiplier := sat(8)
	if p.osaka {
		multiplier = 16
	}
	if h.expLen > modexpLengthWord {
		iterations = multiplier.mul(sat(h.expLen - modexpLengthWord))
	}
	if headBits > 0 {
		iterations = iterations.add(sat(headBits - 1))
	}
	iterations = max(iterations, 1)
	if p.eip198 {
		return uint64(eip198Complexity(sat(maxLen)).mul(iterations) / 20)
	}
	if p.osaka {
		return uint64(max(500, complexity.mul(iterations)))
	}
	return uint64(max(200, complexity.mul(iterations)/3))
}

func (p modexpPricing) valid(input []byte) bool {
	if !p.osaka {
		return true
	}
	return !readModexpHeader(input).oversized()
}

type modexpOperandSet struct {
	base []byte
	exp  []byte
	mod  []byte
}

func modexpOperands(input []byte) (modexpOperandSet, bool) {
	h := readModexpHeader(input)
	if h.baseLen > modexpOsakaLimit*1024 || h.expLen > modexpOsakaLimit*1024 || h.modLen > modexpOsakaLimit*1024 {
		return modexpOperandSet{}, false
	}
	base := paddedSlice(input, memRange{offset: 3 * modexpLengthWord, size: h.baseLen})
	exp := paddedSlice(input, memRange{offset: 3*modexpLengthWord + h.baseLen, size: h.expLen})
	mod := paddedSlice(input, memRange{offset: 3*modexpLengthWord + h.baseLen + h.expLen, size: h.modLen})
	return modexpOperandSet{base: base, exp: exp, mod: mod}, true
}

var blsG1Discounts = [blsDiscountsCount]uint64{
	1000, 949, 848, 797, 764, 750, 738, 728, 719, 712, 705, 698, 692, 687, 682, 677,
	673, 669, 665, 661, 658, 654, 651, 648, 645, 642, 640, 637, 635, 632, 630, 627,
	625, 623, 621, 619, 617, 615, 613, 611, 609, 608, 606, 604, 603, 601, 599, 598,
	596, 595, 593, 592, 591, 589, 588, 586, 585, 584, 582, 581, 580, 579, 577, 576,
	575, 574, 573, 572, 570, 569, 568, 567, 566, 565, 564, 563, 562, 561, 560, 559,
	558, 557, 556, 555, 554, 553, 552, 551, 550, 549, 548, 547, 547, 546, 545, 544,
	543, 542, 541, 540, 540, 539, 538, 537, 536, 536, 535, 534, 533, 532, 532, 531,
	530, 529, 528, 528, 527, 526, 525, 525, 524, 523, 522, 522, 521, 520, 520, 519,
}

var blsG2Discounts = [blsDiscountsCount]uint64{
	1000, 1000, 923, 884, 855, 832, 812, 796, 782, 770, 759, 749, 740, 732, 724, 717,
	711, 704, 699, 693, 688, 683, 679, 674, 670, 666, 663, 659, 655, 652, 649, 646,
	643, 640, 637, 634, 632, 629, 627, 624, 622, 620, 618, 615, 613, 611, 609, 607,
	606, 604, 602, 600, 598, 597, 595, 593, 592, 590, 589, 587, 586, 584, 583, 582,
	580, 579, 578, 576, 575, 574, 573, 571, 570, 569, 568, 567, 566, 565, 563, 562,
	561, 560, 559, 558, 557, 556, 555, 554, 553, 552, 552, 551, 550, 549, 548, 547,
	546, 545, 545, 544, 543, 542, 541, 541, 540, 539, 538, 537, 537, 536, 535, 535,
	534, 533, 532, 532, 531, 530, 530, 529, 528, 528, 527, 526, 526, 525, 524, 524,
}

type msmPricing struct {
	pairSize  uint64
	baseGas   uint64
	discounts *[blsDiscountsCount]uint64
}

func (p msmPricing) gas(input []byte) uint64 {
	k := uint64(len(input)) / p.pairSize
	if k == 0 {
		return 0
	}
	discount := p.discounts[min(k, blsDiscountsCount)-1]
	return k * p.baseGas * discount / 1000
}
