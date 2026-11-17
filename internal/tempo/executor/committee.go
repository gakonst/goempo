package executor

import (
	"encoding/binary"
	"errors"

	"github.com/gakonst/goempo/internal/tempo/dkg"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
)

const (
	selSetCommitteeMembers uint32 = 0x229bdd9c
	abiHeadWords                  = 2
)

var ErrCommittee = errors.New("executor: current committee system call failed")

func setCommitteeCalldata(o *dkg.Outcome) []byte {
	keys := o.Players
	out := make([]byte, selectorBytes+(abiHeadWords+1+len(keys))*abiWordBytes)
	binary.BigEndian.PutUint32(out, selSetCommitteeMembers)
	word := out[selectorBytes:]
	binary.BigEndian.PutUint64(word[abiWordBytes-8:abiWordBytes], o.Epoch)
	binary.BigEndian.PutUint64(word[2*abiWordBytes-8:2*abiWordBytes], abiHeadWords*abiWordBytes)
	binary.BigEndian.PutUint64(word[3*abiWordBytes-8:3*abiWordBytes], uint64(len(keys)))
	for i := range keys {
		copy(word[(abiHeadWords+1+i)*abiWordBytes:], keys[i][:])
	}
	return out
}

func (x *Executor) committeeCall(st State, env *systemEnv) error {
	if !env.rules.AtLeast(hardfork.T8) || x.EpochLength == 0 {
		return nil
	}
	h := &env.header.Inner
	if (h.Number+1)%x.EpochLength != 0 {
		return nil
	}
	o, err := dkg.Decode(h.ExtraData)
	if err != nil {
		return err
	}
	res := run(st, env.config, env.block, systemCall{
		caller: Address{},
		to:     precompiles.CurrentCommitteeAddress,
		input:  setCommitteeCalldata(&o),
		gas:    env.rules.SystemCallGas(),
	})
	st.FinalizeTx()
	if !res.Status.Success() {
		return ErrCommittee
	}
	return nil
}
