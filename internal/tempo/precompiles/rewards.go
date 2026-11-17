package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

type rewardInfo struct {
	recipient      Address
	rewardPerToken Word
	balance        Word
}

type optDir uint8

const (
	optIn optDir = iota
	optOut
)

func (t Token) rewardSlot(holder Address) slot { return t.c.mapAddr(holder, slotUserRewardInfo) }

func (t Token) readRewardInfo(holder Address) (rewardInfo, error) {
	base := t.rewardSlot(holder)
	r, err := t.c.loadAddress(t.addr, base)
	if err != nil {
		return rewardInfo{}, err
	}
	rpt, err := t.load(base.plus(1))
	if err != nil {
		return rewardInfo{}, err
	}
	bal, err := t.load(base.plus(2))
	if err != nil {
		return rewardInfo{}, err
	}
	return rewardInfo{recipient: r, rewardPerToken: rpt, balance: bal}, nil
}

func (t Token) writeRewardInfo(holder Address, info rewardInfo) error {
	base := t.rewardSlot(holder)
	if err := t.c.storeRun(t.addr, base, lane{f: fNextQuote, v: toWord(addressWord(info.recipient))}); err != nil {
		return err
	}
	if err := t.store(base.plus(1), info.rewardPerToken); err != nil {
		return err
	}
	return t.store(base.plus(2), info.balance)
}

func (t Token) accrued(holder Address, delta Word) (Word, error) {
	bal, err := t.balance(holder)
	if err != nil {
		return Word{}, err
	}
	return ratio{num: delta, den: accPrecision}.of(bal)
}

func (t Token) creditReward(info *rewardInfo, reward Word) error {
	b, err := num(info.balance).add(reward)
	info.balance = b
	return err
}

func (t Token) updateRewards(holder Address) (Address, error) {
	if t.c.at(hardfork.T8) {
		return Address{}, nil
	}
	info, err := t.readRewardInfo(holder)
	if err != nil {
		return Address{}, err
	}
	delegate := info.recipient
	global, err := t.load(slotGlobalRPT)
	if err != nil {
		return Address{}, err
	}
	delta, err := num(global).sub(info.rewardPerToken)
	if err != nil || delta.IsZero() {
		return delegate, err
	}
	if delegate != (Address{}) {
		reward, err := t.accrued(holder, delta)
		if err != nil {
			return Address{}, err
		}
		if delegate == holder {
			err = t.creditReward(&info, reward)
		} else {
			err = t.creditDelegate(delegate, reward)
		}
		if err != nil {
			return Address{}, err
		}
	}
	info.rewardPerToken = global
	return delegate, t.writeRewardInfo(holder, info)
}

func (t Token) creditDelegate(delegate Address, reward Word) error {
	d, err := t.readRewardInfo(delegate)
	if err != nil {
		return err
	}
	if err := t.creditReward(&d, reward); err != nil {
		return err
	}
	return t.writeRewardInfo(delegate, d)
}

func (t Token) optedInSupply() (Word, error) { return t.c.loadField(t.addr, slotOptedInSupply, fU128) }

func (t Token) adjustOptedIn(amount Word, dir optDir) error {
	cur, err := t.optedInSupply()
	if err != nil {
		return err
	}
	var next Word
	if dir == optIn {
		next, err = num(cur).add(amount)
	} else {
		next, err = num(cur).sub(amount)
	}
	if err != nil {
		return err
	}
	if next.Gt(u128Max) {
		return underOverflow()
	}
	return t.store(slotOptedInSupply, next)
}

func (t Token) rewardsOnTransfer(h hop, amount Word) error {
	fromDelegate, err := t.updateRewards(h.from)
	if err != nil {
		return err
	}
	toDelegate, err := t.updateRewards(h.to)
	if err != nil {
		return err
	}
	fromIn := fromDelegate != (Address{})
	toIn := toDelegate != (Address{})
	if fromIn == toIn {
		return nil
	}
	if fromIn {
		return t.adjustOptedIn(amount, optOut)
	}
	return t.adjustOptedIn(amount, optIn)
}

func (t Token) rewardsOnMint(to Address, amount Word) error {
	d, err := t.updateRewards(to)
	if err != nil || d == (Address{}) {
		return err
	}
	return t.adjustOptedIn(amount, optIn)
}

func (t Token) distributeReward(sender Address, amount Word) error {
	if t.c.at(hardfork.T7) {
		return nil
	}
	if err := t.checkNotPaused(); err != nil {
		return err
	}
	if amount.IsZero() {
		return errSel("InvalidAmount()")
	}
	if err := t.ensureTransferAuthorized(hop{from: sender, to: t.addr}); err != nil {
		return err
	}
	if err := t.c.authorizeTransfer(tokenFlow{account: sender, token: t.addr, amount: amount}); err != nil {
		return err
	}
	if err := t.move(sender, direct(t.addr), amount); err != nil {
		return err
	}
	opted, err := t.optedInSupply()
	if err != nil {
		return err
	}
	if opted.IsZero() {
		return errSel("NoOptedInSupply()")
	}
	delta, err := ratio{num: accPrecision, den: opted}.of(amount)
	if err != nil {
		return err
	}
	global, err := t.load(slotGlobalRPT)
	if err != nil {
		return err
	}
	next, err := num(global).add(delta)
	if err != nil {
		return err
	}
	if err := t.store(slotGlobalRPT, next); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evRewardDistributed, addressWord(sender)}, encodeWords(toHash(amount)))
}

type rewardDelegation struct {
	holder    Address
	recipient Address
}

func (t Token) setRewardRecipient(d rewardDelegation) error {
	if t.c.at(hardfork.T7) {
		return nil
	}
	if err := t.checkNotPaused(); err != nil {
		return err
	}
	if t.c.at(hardfork.T3) && IsVirtual(d.recipient) {
		return errSel("InvalidRecipient()")
	}
	if d.recipient != (Address{}) {
		if err := t.ensureTransferAuthorized(hop{from: d.holder, to: d.recipient}); err != nil {
			return err
		}
	}
	from, err := t.updateRewards(d.holder)
	if err != nil {
		return err
	}
	bal, err := t.balance(d.holder)
	if err != nil {
		return err
	}
	wasIn := from != (Address{})
	nowIn := d.recipient != (Address{})
	if wasIn && !nowIn {
		err = t.adjustOptedIn(bal, optOut)
	}
	if !wasIn && nowIn {
		err = t.adjustOptedIn(bal, optIn)
	}
	if err != nil {
		return err
	}
	info, err := t.readRewardInfo(d.holder)
	if err != nil {
		return err
	}
	info.recipient = d.recipient
	if err := t.writeRewardInfo(d.holder, info); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evRewardRecipientSet, addressWord(d.holder), addressWord(d.recipient)}, nil)
}

func (t Token) claimRewards(sender Address) (Word, error) {
	if err := t.checkNotPaused(); err != nil {
		return Word{}, err
	}
	if err := t.ensureTransferAuthorized(hop{from: t.addr, to: sender}); err != nil {
		return Word{}, err
	}
	delegate, err := t.updateRewards(sender)
	if err != nil {
		return Word{}, err
	}
	info, err := t.readRewardInfo(sender)
	if err != nil {
		return Word{}, err
	}
	contract, err := t.balance(t.addr)
	if err != nil {
		return Word{}, err
	}
	amount := info.balance
	if contract.Lt(amount) {
		amount = contract
	}
	info.balance = info.balance.Sub(amount)
	if err := t.writeRewardInfo(sender, info); err != nil {
		return Word{}, err
	}
	if amount.IsZero() {
		return amount, nil
	}
	if err := t.store(t.balanceSlot(t.addr), contract.Sub(amount)); err != nil {
		return Word{}, err
	}
	bal, err := t.balance(sender)
	if err != nil {
		return Word{}, err
	}
	nb, err := num(bal).add(amount)
	if err != nil {
		return Word{}, err
	}
	if err := t.store(t.balanceSlot(sender), nb); err != nil {
		return Word{}, err
	}
	if delegate != (Address{}) {
		if err := t.adjustOptedIn(amount, optIn); err != nil {
			return Word{}, err
		}
	}
	return amount, t.emitTransfer(transferEvent{from: t.addr, to: sender, amount: amount})
}

func (t Token) selfAccrued(account Address, info rewardInfo) (Word, error) {
	bal, err := t.balance(account)
	if err != nil || bal.IsZero() {
		return info.balance, err
	}
	global, err := t.load(slotGlobalRPT)
	if err != nil {
		return Word{}, err
	}
	delta, err := num(global).sub(info.rewardPerToken)
	if err != nil {
		return Word{}, err
	}
	acc, err := ratio{num: delta, den: accPrecision}.of(bal)
	if err != nil {
		return Word{}, err
	}
	return num(info.balance).add(acc)
}

func (t Token) pendingRewards(account Address) (Word, error) {
	info, err := t.readRewardInfo(account)
	if err != nil {
		return Word{}, err
	}
	pending := info.balance
	if !t.c.at(hardfork.T8) && info.recipient == account {
		if pending, err = t.selfAccrued(account, info); err != nil {
			return Word{}, err
		}
	}
	if pending.Gt(u128Max) {
		return Word{}, underOverflow()
	}
	return pending, nil
}
