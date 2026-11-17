package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

const (
	cacheFileName  = "blocks.jsonl"
	cacheBlocksMax = 1 << 20
	cacheBytesMax  = 1 << 40
)

const _ = uint(cacheBlocksMax - fetchBlocksMax)

const _ = uint(cacheBytesMax - fetchBlocksMax*blockJSONMax)

var errCacheTooLarge = errors.New("block cache exceeds its limit")

type cachedBlock struct {
	Number   uint64          `json:"number"`
	Raw      string          `json:"raw"`
	Block    json.RawMessage `json:"block"`
	Receipts json.RawMessage `json:"receipts"`
}

func cachePath(dir string) string {
	return filepath.Join(dir, cacheFileName)
}

func loadCache(dir string) ([]cachedBlock, error) {
	data, err := os.ReadFile(cachePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > cacheBytesMax {
		return nil, errCacheTooLarge
	}
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > cacheBlocksMax {
		return nil, errCacheTooLarge
	}
	byNumber := make(map[uint64]cachedBlock, len(lines))
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var b cachedBlock
		if err := json.Unmarshal(line, &b); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", cacheFileName, i+1, err)
		}
		byNumber[b.Number] = b
	}
	numbers := slices.Sorted(maps.Keys(byNumber))
	out := make([]cachedBlock, len(numbers))
	for i, n := range numbers {
		out[i] = byNumber[n]
	}
	return out, nil
}

func appendCache(dir string, blocks []cachedBlock) error {
	var buf bytes.Buffer
	for i := range blocks {
		line, err := json.Marshal(&blocks[i])
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(cachePath(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
