package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

func reserveMutate(sig string, args []abiType, fn handler) method {
	return mutate(sig, args, func(c *Context, a []abiValue) ([]byte, error) {
		if err := c.preserveCredits(ChannelReserveAddress); err != nil {
			return nil, err
		}
		return fn(c, a)
	}).from(hardfork.T5)
}

func reserveView(sig string, args []abiType, fn handler) method {
	return view(sig, args, fn).from(hardfork.T5)
}

func descriptorArgs(extra ...abiType) []abiType {
	return append(append([]abiType{}, reserveDescriptorArgs...), extra...)
}

const reserveDescriptorSig = "(address,address,address,address,bytes32,address,bytes32)"

func (c *Context) channelStatesBatch(ids [][]Hash) ([]byte, error) {
	if len(ids) > maxArrayItems {
		return nil, errABI
	}
	out := make([]Hash, 0, 2+3*len(ids))
	out = append(out, wordOfU64(32), wordOfU64(uint64(len(ids))))
	for _, id := range ids {
		s, err := c.loadChannelState(id[0])
		if err != nil {
			return nil, err
		}
		out = append(out, s.encode()...)
	}
	return encodeWords(out...), nil
}

var channelReserveTable = register(ChannelReserveAddress, newTable(
	reserveView("CLOSE_GRACE_PERIOD()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		return retU64(reserveCloseGrace), nil
	}),
	reserveView("VOUCHER_TYPEHASH()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		return encodeWords(reserveVoucherTH), nil
	}),
	reserveMutate("open(address,address,address,uint96,bytes32,address)", []abiType{tAddress, tAddress, tAddress, tU96, tB32, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		d := channelDescriptor{payer: c.Caller, payee: a[0].addr(), operator: a[1].addr(), token: a[2].addr(), salt: a[4].bytes32(), signer: a[5].addr()}
		id, err := c.openChannel(openReq{desc: d, deposit: a[3].u256()})
		return encodeWords(id), err
	}),
	reserveMutate("settle("+reserveDescriptorSig+",uint96,bytes)", descriptorArgs(tU96, tBytes), func(c *Context, a []abiValue) ([]byte, error) {
		n := reserveDescriptorArgsLen
		return nil, c.settleChannel(settleReq{sender: c.Caller, desc: descriptorOf(a), amount: a[n].u256(), sig: a[n+1].data})
	}),
	reserveMutate("topUp("+reserveDescriptorSig+",uint96)", descriptorArgs(tU96), func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.topUpChannel(topUpReq{sender: c.Caller, desc: descriptorOf(a), amount: a[reserveDescriptorArgsLen].u256()})
	}),
	reserveMutate("close("+reserveDescriptorSig+",uint96,uint96,bytes)", descriptorArgs(tU96, tU96, tBytes), func(c *Context, a []abiValue) ([]byte, error) {
		n := reserveDescriptorArgsLen
		return nil, c.closeChannel(closeReq{sender: c.Caller, desc: descriptorOf(a), amount: a[n].u256(), capture: a[n+1].u256(), sig: a[n+2].data})
	}),
	reserveMutate("requestClose("+reserveDescriptorSig+")", descriptorArgs(), func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.requestCloseChannel(channelAction{sender: c.Caller, desc: descriptorOf(a)})
	}),
	reserveMutate("withdraw("+reserveDescriptorSig+")", descriptorArgs(), func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.withdrawChannel(channelAction{sender: c.Caller, desc: descriptorOf(a)})
	}),
	reserveView("getChannel("+reserveDescriptorSig+")", descriptorArgs(), func(c *Context, a []abiValue) ([]byte, error) {
		d := descriptorOf(a)
		id, err := c.channelID(d)
		if err != nil {
			return nil, err
		}
		s, err := c.loadChannelState(id)
		return encodeWords(append(d.words(), s.encode()...)...), err
	}),
	reserveView("getChannelState(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		s, err := c.loadChannelState(a[0].bytes32())
		return encodeWords(s.encode()...), err
	}),
	reserveView("getChannelStatesBatch(bytes32[])", []abiType{tScalarArray(tB32)}, func(c *Context, a []abiValue) ([]byte, error) {
		return c.channelStatesBatch(a[0].items)
	}),
	reserveView("computeChannelId(address,address,address,address,bytes32,address,bytes32)", descriptorArgs(), func(c *Context, a []abiValue) ([]byte, error) {
		id, err := c.channelID(descriptorOf(a))
		return encodeWords(id), err
	}),
	reserveView("getVoucherDigest(bytes32,uint96)", []abiType{tB32, tU96}, func(c *Context, a []abiValue) ([]byte, error) {
		h, err := c.voucherDigest(voucher{id: a[0].bytes32(), cumulative: a[1].u256()})
		return encodeWords(h), err
	}),
	reserveView("domainSeparator()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		return encodeWords(c.reserveDomainSeparator()), nil
	}),
	view("storageCredits(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		w, err := c.sload(ChannelReserveAddress, c.reserveCreditSlot(a[0].addr()))
		return retWord(w), err
	}).from(hardfork.T7),
))
