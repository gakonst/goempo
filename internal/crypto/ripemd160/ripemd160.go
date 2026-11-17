package ripemd160

import "golang.org/x/crypto/ripemd160"

const (
	OutputLength = 32
	BaseGas      = 600
	WordGas      = 120
	wordLength   = 32
	digestOffset = OutputLength - ripemd160.Size
)

const _ = uint(BaseGas - WordGas)

func Gas(inputLength int) uint64 {
	words := (uint64(inputLength) + wordLength - 1) / wordLength
	return BaseGas + WordGas*words
}

func Sum(input []byte) [OutputLength]byte {
	h := ripemd160.New()
	h.Write(input)
	var out [OutputLength]byte
	copy(out[digestOffset:], h.Sum(nil))
	return out
}
