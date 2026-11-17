package precompiles

import (
	"github.com/gakonst/goempo/internal/u256"
)

var (
	slotGuardNonce    = slotOf(0)
	slotGuardBalances = slotOf(1)
	slotRegistryData  = slotOf(0)
)

const (
	inboundTransfer uint8 = 0
	inboundMint     uint8 = 1
)

var evTransferBlocked = topic("TransferBlocked(address,address,uint64,uint256,uint8,bytes)")

func masterKey(id [4]byte) Hash {
	var h Hash
	copy(h[28:], id[:])
	return h
}

func (c *Context) registryMaster(id [4]byte) (Address, error) {
	return c.loadAddress(AddressRegistryAddress, c.mapKey(masterKey(id), slotRegistryData))
}

func (c *Context) nextReceiptNonce() (uint64, error) {
	w, err := c.sload(ReceivePolicyGuardAddress, slotGuardNonce)
	if err != nil {
		return 0, err
	}
	n := max(fU64At0.u64(w), 1)
	if n == ^uint64(0) {
		return 0, underOverflow()
	}
	return n, c.sstore(ReceivePolicyGuardAddress, slotGuardNonce, u256.FromUint64(n+1))
}

func (c *Context) blockInbound(t Token, req inboundReq, block receiveBlock) error {
	guard := direct(ReceivePolicyGuardAddress)
	kind := inboundTransfer
	if req.mintSupply != nil {
		if err := t.mintInto(mintInto{to: guard, supply: *req.mintSupply, amount: req.amount}); err != nil {
			return err
		}
		if err := t.emitMint(ReceivePolicyGuardAddress, req.amount); err != nil {
			return err
		}
		kind = inboundMint
	} else if err := t.move(req.originator, guard, req.amount); err != nil {
		return err
	}
	nonce, err := c.nextReceiptNonce()
	if err != nil {
		return err
	}
	receipt := encodeWords(
		wordOfU64(1),
		addressWord(t.addr),
		addressWord(block.recovery),
		addressWord(req.originator),
		addressWord(req.to.original()),
		wordOfU64(c.timestamp()),
		wordOfU64(nonce),
		wordOfU64(uint64(block.reason)),
		wordOfU64(uint64(kind)),
		req.memo,
	)
	if err := c.chargeKeccak(len(receipt)); err != nil {
		return err
	}
	key := c.keccak(receipt)
	if err := c.sstore(ReceivePolicyGuardAddress, c.mapKey(key, slotGuardBalances), req.amount); err != nil {
		return err
	}
	data := newEncoder(3).u256(req.amount).u64(1).bytes(receipt).out()
	return c.emit(ReceivePolicyGuardAddress, []Hash{evTransferBlocked, addressWord(t.addr), addressWord(req.to.target), wordOfU64(nonce)}, data)
}
