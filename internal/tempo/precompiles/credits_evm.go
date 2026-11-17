package precompiles

type EVMStore struct {
	Owner   Address
	Key     Hash
	Present Word
	Next    Word
}

func EVMStorageCredits(c *Context, gas uint64, s EVMStore) (uint64, error) {
	c.gasLimit = gas
	c.gasUsed = 0
	err := c.storageCredits(s.Owner, slot(s.Key), sstoreValues{present: s.Present, next: s.Next})
	return c.gasUsed, err
}
