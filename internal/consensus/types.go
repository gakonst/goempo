package consensus

type Round struct {
	Epoch uint64
	View  uint64
}

type Proposal struct {
	Round   Round
	Parent  uint64
	Payload Digest
}

func (r Round) Encode() []byte {
	return appendVarint(appendVarint(make([]byte, 0, 2*varintBytesMax), r.Epoch), r.View)
}

func (p Proposal) Encode() []byte {
	out := make([]byte, 0, 3*varintBytesMax+DigestBytes)
	out = append(out, p.Round.Encode()...)
	out = appendVarint(out, p.Parent)
	return append(out, p.Payload[:]...)
}

func readRound(r *reader) (Round, error) {
	epoch, err := r.varint()
	if err != nil {
		return Round{}, err
	}
	view, err := r.varint()
	if err != nil {
		return Round{}, err
	}
	return Round{Epoch: epoch, View: view}, nil
}

func readProposal(r *reader) (Proposal, error) {
	round, err := readRound(r)
	if err != nil {
		return Proposal{}, err
	}
	parent, err := r.varint()
	if err != nil {
		return Proposal{}, err
	}
	payload, err := r.fixed(DigestBytes)
	if err != nil {
		return Proposal{}, err
	}
	p := Proposal{Round: round, Parent: parent}
	copy(p.Payload[:], payload)
	return p, nil
}

type Certificate struct {
	Vote [SignatureBytes]byte
	Seed [SignatureBytes]byte
}

func readCertificate(r *reader) (Certificate, error) {
	var c Certificate
	vote, err := r.fixed(SignatureBytes)
	if err != nil {
		return c, err
	}
	seed, err := r.fixed(SignatureBytes)
	if err != nil {
		return c, err
	}
	copy(c.Vote[:], vote)
	copy(c.Seed[:], seed)
	return c, nil
}

func (c Certificate) append(out []byte) []byte {
	return append(append(out, c.Vote[:]...), c.Seed[:]...)
}

type Finalization struct {
	Proposal    Proposal
	Certificate Certificate
}

func DecodeFinalization(data []byte) (Finalization, error) {
	r := reader{data: data}
	p, err := readProposal(&r)
	if err != nil {
		return Finalization{}, err
	}
	c, err := readCertificate(&r)
	if err != nil {
		return Finalization{}, err
	}
	if err := r.done(); err != nil {
		return Finalization{}, err
	}
	return Finalization{Proposal: p, Certificate: c}, nil
}

func (f Finalization) Encode() []byte {
	return f.Certificate.append(f.Proposal.Encode())
}
