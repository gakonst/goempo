package state_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/u256"
)

type diffAccount struct {
	Balance *string           `json:"balance"`
	Nonce   *uint64           `json:"nonce"`
	Code    *string           `json:"code"`
	Storage map[string]string `json:"storage"`
}

type diffTx struct {
	Pre  map[string]diffAccount `json:"pre"`
	Post map[string]diffAccount `json:"post"`
}

type diffBlock struct {
	N    uint64   `json:"n"`
	Root string   `json:"root"`
	Txs  []diffTx `json:"txs"`
}

func parseHex(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	if len(s)%2 == 1 {
		s = "0" + s
	}
	return hex.DecodeString(s)
}

func parseAddress(s string) (primitives.Address, error) {
	var a primitives.Address
	b, err := parseHex(s)
	if err != nil || len(b) != len(a) {
		return a, fmt.Errorf("bad address %q", s)
	}
	copy(a[:], b)
	return a, nil
}

func parseWord(s string) (u256.Int, error) {
	b, err := parseHex(s)
	if err != nil || len(b) > 32 {
		return u256.Int{}, fmt.Errorf("bad word %q", s)
	}
	return u256.FromBytes(b), nil
}

func parseHash(s string) (primitives.Hash, error) {
	w, err := parseWord(s)
	return primitives.Hash(w.Bytes32()), err
}

func applyPost(st *state.State, addr primitives.Address, post diffAccount) error {
	if post.Balance != nil {
		v, err := parseWord(*post.Balance)
		if err != nil {
			return err
		}
		st.SubBalance(addr, st.Balance(addr))
		st.AddBalance(addr, v)
	}
	if post.Nonce != nil {
		st.SetNonce(addr, *post.Nonce)
	}
	if post.Code != nil {
		code, err := parseHex(*post.Code)
		if err != nil {
			return err
		}
		st.SetCode(addr, code)
	}
	for _, k := range slices.Sorted(maps.Keys(post.Storage)) {
		key, err := parseHash(k)
		if err != nil {
			return err
		}
		v, err := parseWord(post.Storage[k])
		if err != nil {
			return err
		}
		st.SetStorage(addr, key, v)
	}
	return nil
}

func applyDiffTx(st *state.State, tx diffTx) error {
	for _, k := range slices.Sorted(maps.Keys(tx.Post)) {
		addr, err := parseAddress(k)
		if err != nil {
			return err
		}
		if err := applyPost(st, addr, tx.Post[k]); err != nil {
			return err
		}
	}
	for _, k := range slices.Sorted(maps.Keys(tx.Pre)) {
		addr, err := parseAddress(k)
		if err != nil {
			return err
		}
		post, ok := tx.Post[k]
		if !ok {
			st.SelfDestruct(addr)
			continue
		}
		for _, sk := range slices.Sorted(maps.Keys(tx.Pre[k].Storage)) {
			if _, kept := post.Storage[sk]; kept {
				continue
			}
			key, err := parseHash(sk)
			if err != nil {
				return err
			}
			st.SetStorage(addr, key, u256.Zero())
		}
	}
	st.FinalizeTx()
	return nil
}

const diffLinesMax = 1 << 26

const _ = uint(math.MaxInt32 - diffLinesMax)

type diffReplay struct {
	db     *state.DB
	blocks int
}

func (r *diffReplay) replay(src io.Reader) error {
	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<30)
	st := r.db.State()
	lines := 0
	for ; lines < diffLinesMax; lines++ {
		if !sc.Scan() {
			break
		}
		var b diffBlock
		if err := json.Unmarshal(sc.Bytes(), &b); err != nil {
			return err
		}
		if b.N <= r.db.Head().Number {
			continue
		}
		if b.N != r.db.Head().Number+1 && r.db.Head().Number != 0 {
			return fmt.Errorf("block %d follows head %d", b.N, r.db.Head().Number)
		}
		for _, tx := range b.Txs {
			if err := applyDiffTx(st, tx); err != nil {
				return fmt.Errorf("block %d: %w", b.N, err)
			}
		}
		root, err := st.Commit(b.N)
		if err != nil {
			return err
		}
		want, err := parseHash(b.Root)
		if err != nil {
			return err
		}
		if root != want {
			return fmt.Errorf("block %d state root %s want %s", b.N, root, want)
		}
		r.blocks++
	}
	if lines == diffLinesMax {
		return errors.New("state diff file holds more than diffLinesMax blocks")
	}
	return sc.Err()
}

func genesisDB(t testing.TB, fs goio.FS) *state.DB {
	t.Helper()
	g, err := genesis.Mainnet()
	must(t, err)
	db := openDB(t, fs, state.Options{})
	_, err = db.LoadGenesis(g)
	must(t, err)
	return db
}

func TestMainnetStateDiffs(t *testing.T) {
	name := os.Getenv("GOEMPO_STATEDIFF_FILE")
	if name == "" {
		name = "testdata/mainnet-statediff-3000-4999.jsonl.gz"
	}
	raw, err := os.ReadFile(name)
	must(t, err)
	var src io.Reader = bytes.NewReader(raw)
	if strings.HasSuffix(name, ".gz") {
		zr, err := gzip.NewReader(src)
		must(t, err)
		src = zr
	}
	r := diffReplay{db: genesisDB(t, goio.NewMemFS())}
	err = r.replay(src)
	t.Logf("replayed %d blocks to head %+v", r.blocks, r.db.Head())
	must(t, err)
	if r.blocks < 2000 {
		t.Fatalf("replayed only %d blocks", r.blocks)
	}
	must(t, r.db.Close())
}
