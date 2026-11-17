package cwp2p

import (
	"slices"

	"github.com/gakonst/goempo/internal/crypto/blake3"
)

type TranscriptVersion uint8

const (
	TranscriptV0 TranscriptVersion = 0
	TranscriptV1 TranscriptVersion = 1
)

const (
	tagNew      = 0
	tagResume   = 1
	tagFork     = 2
	tagNoise    = 3
	SummarySize = blake3.Size
)

type Summary [SummarySize]byte

type Transcript struct {
	h       *blake3.Hasher
	pending uint64
	version TranscriptVersion
}

func startTranscript(tag byte, key *Summary, version TranscriptVersion) *Transcript {
	t := &Transcript{version: version}
	if key == nil {
		t.h = blake3.New()
	} else {
		t.h = blake3.NewKeyed(*key)
	}
	t.h.Write([]byte{tag})
	return t
}

func NewTranscript(namespace []byte, version TranscriptVersion) *Transcript {
	t := startTranscript(tagNew, nil, version)
	t.Commit(namespace)
	return t
}

func ResumeTranscript(s Summary, version TranscriptVersion) *Transcript {
	return startTranscript(tagResume, &s, version)
}

func writeLength(h *blake3.Hasher, pending uint64, version TranscriptVersion) {
	length := AppendVarint(make([]byte, 0, varintBytesMax), pending)
	if version == TranscriptV1 {
		slices.Reverse(length)
	}
	h.Write(length)
}

func (t *Transcript) Append(data []byte) *Transcript {
	t.h.Write(data)
	t.pending += uint64(len(data))
	return t
}

func (t *Transcript) Commit(data []byte) *Transcript {
	t.Append(data)
	writeLength(t.h, t.pending, t.version)
	t.pending = 0
	return t
}

func (t *Transcript) Summarize() Summary {
	if t.pending == 0 {
		return Summary(t.h.Sum())
	}
	h := t.h.Clone()
	writeLength(h, t.pending, t.version)
	return Summary(h.Sum())
}

func (t *Transcript) Fork(label string) *Transcript {
	s := t.Summarize()
	out := startTranscript(tagFork, &s, t.version)
	out.Commit([]byte(label))
	return out
}

func (t *Transcript) Noise(label string, out []byte) {
	s := t.Summarize()
	n := startTranscript(tagNoise, &s, t.version)
	n.Commit([]byte(label))
	n.h.XOF(out)
}
