package cwp2p

const p2pSuffix = "_P2P"

func Namespace(app string) []byte {
	out := AppendVarint(make([]byte, 0, varintBytesMax+len(app)+len(p2pSuffix)), uint64(len(app)))
	out = append(out, app...)
	return append(out, p2pSuffix...)
}

func TempoNamespace() []byte {
	return Namespace("TEMPO")
}
