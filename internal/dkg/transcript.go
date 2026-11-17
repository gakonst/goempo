package dkg

const (
	tagNew     = 0
	tagResume  = 1
	tagFork    = 2
	tagNoise   = 3
	SummaryLen = 32
)

const _ = uint(SummaryLen - blake3OutLen)

const _ = uint(blake3KeyLen - SummaryLen)

type Summary [SummaryLen]byte

type Transcript struct {
	h blake3Hasher
}

func startTranscript(tag byte, key *Summary) Transcript {
	var t Transcript
	if key == nil {
		t.h = newBlake3()
	} else {
		t.h = newBlake3Keyed(*key)
	}
	t.h.write([]byte{tag})
	return t
}

func NewTranscript(namespace []byte) Transcript {
	t := startTranscript(tagNew, nil)
	t.Commit(namespace)
	return t
}

func ResumeTranscript(s Summary) Transcript {
	return startTranscript(tagResume, &s)
}

func (t *Transcript) Commit(data []byte) {
	t.h.write(data)
	t.h.write(appendVarint(nil, uint64(len(data))))
}

func (t *Transcript) Summarize() Summary {
	return Summary(t.h.sum())
}

func (t *Transcript) Fork(label []byte) Transcript {
	s := t.Summarize()
	out := startTranscript(tagFork, &s)
	out.Commit(label)
	return out
}

type Noise struct {
	out      blake3Output
	position uint64
}

func (t *Transcript) Noise(label []byte) *Noise {
	s := t.Summarize()
	n := startTranscript(tagNoise, &s)
	n.Commit(label)
	return &Noise{out: n.h.rootOutput()}
}

func (n *Noise) Read(p []byte) (int, error) {
	n.out.root(n.position, p)
	n.position += uint64(len(p))
	return len(p), nil
}

const (
	varintBytesMax = 10
	varintShift    = 7
	varintMore     = 0x80
	varintLow      = 0x7f
)

const _ = uint(varintBytesMax*varintShift - 64)

func appendVarint(out []byte, v uint64) []byte {
	for range varintBytesMax {
		if v < varintMore {
			return append(out, byte(v))
		}
		out = append(out, byte(v&varintLow)|varintMore)
		v >>= varintShift
	}
	return out
}
