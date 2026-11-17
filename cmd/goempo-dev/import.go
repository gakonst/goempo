package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
)

// runImport loads a state dump written by tempo goempo-dump into an empty
// goempo datadir at the dump's block, and checks the resulting state root
// against the block header's.
func runImport(args []string) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	dump := fs.String("dump", "", "dump file from tempo goempo-dump")
	datadir := fs.String("datadir", "", "empty goempo state directory")
	chunk := fs.Int("chunk-slots", 4_000_000, "commit after this many staged slots")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := importDump(*dump, *datadir, *chunk); err != nil {
		fmt.Fprintln(os.Stderr, "goempo-dev import:", err)
		return 1
	}
	return 0
}

func importDump(path, datadir string, chunk int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 16<<20)
	var hdr [8 + 8 + 32 + 32]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	if string(hdr[:8]) != "GOEMPOD1" {
		return errors.New("not a goempo dump")
	}
	number := binary.BigEndian.Uint64(hdr[8:16])
	var blockHash, wantRoot primitives.Hash
	copy(blockHash[:], hdr[16:48])
	copy(wantRoot[:], hdr[48:80])
	fmt.Fprintf(os.Stderr, "import: block %d %s root %s\n", number, blockHash, wantRoot)
	if err := os.MkdirAll(datadir, 0o755); err != nil {
		return err
	}
	dir, err := goio.NewDirFS(datadir)
	if err != nil {
		return err
	}
	db, err := state.Open(dir, state.Options{MemtableBytes: 256 << 20})
	if err != nil {
		return err
	}
	if db.Head().Loaded {
		return errors.Join(errors.New("datadir is not empty"), db.Close())
	}
	began := time.Now()
	codes := make(map[primitives.Hash][]byte)
	s := db.State()
	var (
		staged, commits     int
		accounts, slotCount uint64
		root                primitives.Hash
		slots               []state.ImportSlot
	)
	commit := func(n uint64) error {
		root, err = s.Commit(n)
		staged = 0
		commits++
		return err
	}
	for {
		tag, err := r.ReadByte()
		if err != nil {
			return errors.Join(fmt.Errorf("dump ends without its trailer: %w", err), db.Close())
		}
		switch tag {
		case 'C':
			var h [36]byte
			if _, err := io.ReadFull(r, h[:]); err != nil {
				return err
			}
			code := make([]byte, binary.BigEndian.Uint32(h[32:]))
			if _, err := io.ReadFull(r, code); err != nil {
				return err
			}
			var ch primitives.Hash
			copy(ch[:], h[:32])
			if keccak.Sum256(code) != ch {
				return fmt.Errorf("code %s does not hash to its key", ch)
			}
			codes[ch] = code
		case 'A':
			var h [20 + 8 + 32 + 32 + 4]byte
			if _, err := io.ReadFull(r, h[:]); err != nil {
				return err
			}
			var addr primitives.Address
			copy(addr[:], h[:20])
			nonce := binary.BigEndian.Uint64(h[20:28])
			var bal [32]byte
			copy(bal[:], h[28:60])
			var ch primitives.Hash
			copy(ch[:], h[60:92])
			n := binary.BigEndian.Uint32(h[92:])
			slots = slots[:0]
			var kv [64]byte
			for range n {
				if _, err := io.ReadFull(r, kv[:]); err != nil {
					return err
				}
				var sl state.ImportSlot
				copy(sl.Key[:], kv[:32])
				copy(sl.Value[:], kv[32:])
				slots = append(slots, sl)
			}
			code, ok := codes[ch]
			if !ok && ch != keccak.Sum256(nil) {
				return fmt.Errorf("account %s has unknown code %s", addr, ch)
			}
			s.ImportAccount(addr, nonce, bal, ch, code, slots)
			accounts++
			slotCount += uint64(n)
			staged += int(n) + 1
			if staged >= chunk {
				if err := commit(uint64(commits + 1)); err != nil {
					return errors.Join(err, db.Close())
				}
				fmt.Fprintf(os.Stderr, "import: %d accounts, %d slots, %s\n", accounts, slotCount, time.Since(began).Round(time.Second))
			}
		case 'E':
			var t [24]byte
			if _, err := io.ReadFull(r, t[:]); err != nil {
				return err
			}
			if binary.BigEndian.Uint64(t[:8]) != accounts || binary.BigEndian.Uint64(t[8:16]) != slotCount || binary.BigEndian.Uint64(t[16:]) != uint64(len(codes)) {
				return errors.Join(errors.New("dump trailer counts do not match its records"), db.Close())
			}
			if err := commit(number); err != nil {
				return errors.Join(err, db.Close())
			}
			if err := db.Flush(); err != nil {
				return errors.Join(err, db.Close())
			}
			fmt.Fprintf(os.Stderr, "import: %d accounts, %d slots, %d codes in %s; root %s\n", accounts, slotCount, len(codes), time.Since(began).Round(time.Second), root)
			if root != wantRoot {
				return errors.Join(fmt.Errorf("state root %s does not match block %d's %s", root, number, wantRoot), db.Close())
			}
			fmt.Fprintf(os.Stderr, "import: state root matches block %d\n", number)
			return db.Close()
		default:
			return errors.Join(fmt.Errorf("unknown record %q", tag), db.Close())
		}
	}
}
