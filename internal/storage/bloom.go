package storage

const bloomBitsPerKey = 10

const bloomProbes = 7

const _ = uint(bloomBitsPerKey - bloomProbes)

type bloom struct {
	words []uint64
}

func newBloom(keys int) bloom {
	bitCount := max(keys*bloomBitsPerKey, 64)
	return bloom{words: make([]uint64, (bitCount+63)/64)}
}

func keyHash(key []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range key {
		h ^= uint64(c)
		h *= 1099511628211
	}
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

func (b bloom) add(key []byte) {
	b.addHash(keyHash(key))
}

func (b bloom) addHash(h uint64) {
	step := h>>32 | 1
	bitCount := uint64(len(b.words)) * 64
	for i := 0; i < bloomProbes; i++ {
		bit := (h + uint64(i)*step) % bitCount
		b.words[bit/64] |= 1 << (bit % 64)
	}
}

func (b bloom) mayContain(key []byte) bool {
	h := keyHash(key)
	step := h>>32 | 1
	bitCount := uint64(len(b.words)) * 64
	for i := 0; i < bloomProbes; i++ {
		bit := (h + uint64(i)*step) % bitCount
		if b.words[bit/64]&(1<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}
