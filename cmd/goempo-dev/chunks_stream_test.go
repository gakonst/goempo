package main_test

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	dev "github.com/gakonst/goempo/cmd/goempo-dev"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"go.uber.org/goleak"
)

// Repeat the recorded mainnet archive for decoder scheduling, not consensus.
// Consensus equivalence on the original six blocks remains checked by replay.
type streamInput struct {
	n   int
	bad int
}

func streamFixture(t *testing.T, in streamInput) string {
	n, bad := in.n, in.bad
	t.Helper()
	fs, err := goio.NewDirFS("../../internal/node/testdata")
	if err != nil {
		t.Fatal(err)
	}
	raws, err := node.NewArchive(fs).RawBlocks(0, 6)
	if err != nil {
		t.Fatal(err)
	}
	var chunk bytes.Buffer
	for i := range n {
		raw := raws[i%len(raws)]
		if i == bad {
			raw = []byte{0xc0}
		}
		if err := binary.Write(&chunk, binary.BigEndian, uint32(len(raw))); err != nil {
			t.Fatal(err)
		}
		chunk.Write(raw)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0000000000.blk"), chunk.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStreamingReplayStopsWithUnreadWindows(t *testing.T) {
	defer goleak.VerifyNone(t)
	t.Run("serial", func(t *testing.T) {
		dir := streamFixture(t, streamInput{n: 10_000, bad: 65})
		result := filepath.Join(t.TempDir(), "result.json")
		code := dev.Run([]string{"replay", "-dir", dir, "-to", "5", "-heal", "0", "-diagnose", "off", "-result", result})
		streamRecordedResult(t, result, code)

	})
}

func TestStreamingReplayCancelsPrefetchesOnFailure(t *testing.T) {
	defer goleak.VerifyNone(t)
	t.Run("serial", func(t *testing.T) {
		dir := streamFixture(t, streamInput{n: 10_000, bad: -1})
		streamChunkLink(t, dir, 10_000)
		streamChunkLink(t, dir, 20_000)
		streamChunkLink(t, dir, 30_000)
		result := filepath.Join(t.TempDir(), "result.json")
		code := dev.Run([]string{"replay", "-dir", dir, "-to", "39999", "-heal", "0", "-diagnose", "off", "-result", result})
		if code != 1 {
			t.Fatalf("repeated archive is not a valid chain: exit %d", code)
		}
		raw, err := os.ReadFile(result)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Height   uint64 `json:"height"`
			Mismatch string `json:"mismatch"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Height != 5 || got.Mismatch == "" {
			t.Fatalf("first invalid block must stop at accepted height 5: %+v", got)
		}
	})
}

func streamChunkLink(t *testing.T, dir string, start int) {
	t.Helper()
	if err := os.Link(filepath.Join(dir, "0000000000.blk"), filepath.Join(dir, fmt.Sprintf("%010d.blk", start))); err != nil {
		t.Fatal(err)
	}
}

func TestStreamingRecordedReplay(t *testing.T) {
	defer goleak.VerifyNone(t)
	for _, format := range []string{"raw", "compressed"} {
		t.Run(format, func(t *testing.T) {
			dir := streamFixture(t, streamInput{n: 6, bad: -1})
			if format == "compressed" {
				streamCompress(t, dir)
			}
			result := filepath.Join(t.TempDir(), "result.json")
			code := dev.Run([]string{"replay", "-dir", dir, "-to", "5", "-heal", "0", "-diagnose", "off", "-result", result})
			streamRecordedResult(t, result, code)
		})
	}
}

func streamRecordedResult(t *testing.T, path string, code int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Height, Target uint64
		Mismatch       string
		Bench          struct{ Blocks uint64 }
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if code != 0 || got.Height != 5 || got.Target != 5 || got.Mismatch != "" || got.Bench.Blocks != 5 {
		t.Fatalf("recorded replay: exit=%d result=%+v", code, got)
	}
}

func streamCompress(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "0000000000.blk")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var packed bytes.Buffer
	writer, err := flate.NewWriter(&packed, flate.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"z", packed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
