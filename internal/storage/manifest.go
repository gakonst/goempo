package storage

import (
	"errors"
	"hash/crc32"
	"strconv"
	"strings"

	goio "github.com/gakonst/goempo/internal/io"
)

const manifestName = "MANIFEST"

const manifestTemp = "MANIFEST.tmp"

const manifestHeaderV1 = "goempo-storage v1"

const manifestHeader = "goempo-storage v2"

const runPrefix = "run "

type manifest struct {
	runs [][]string
	seq  uint64
}

func readManifest(fs goio.FS) (manifest, error) {
	file, err := fs.Open(manifestName)
	if errors.Is(err, goio.ErrNotExist) {
		return manifest{}, nil
	}
	if err != nil {
		return manifest{}, err
	}
	raw := make([]byte, file.Size())
	_, err = file.ReadAt(raw, 0)
	if err = errors.Join(err, file.Close()); err != nil {
		return manifest{}, err
	}
	return parseManifest(string(raw))
}

func parseManifest(text string) (manifest, error) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) < 3 {
		return manifest{}, ErrManifest
	}
	if lines[0] != manifestHeader && lines[0] != manifestHeaderV1 {
		return manifest{}, ErrManifest
	}
	body := strings.Join(lines[:len(lines)-1], "\n")
	sum := strconv.FormatUint(uint64(crc32.Checksum([]byte(body), castagnoli)), 10)
	if lines[len(lines)-1] != "crc "+sum {
		return manifest{}, ErrManifest
	}
	seq, err := strconv.ParseUint(strings.TrimPrefix(lines[1], "seq "), 10, 64)
	if err != nil {
		return manifest{}, ErrManifest
	}
	entries := lines[2 : len(lines)-1]
	if len(entries) > segmentsMax {
		return manifest{}, ErrManifest
	}
	man := manifest{seq: seq, runs: make([][]string, 0, len(entries))}
	for _, line := range entries {
		if lines[0] == manifestHeaderV1 {
			man.runs = append(man.runs, []string{line})
			continue
		}
		names := strings.Fields(strings.TrimPrefix(line, runPrefix))
		if !strings.HasPrefix(line, runPrefix) || len(names) == 0 || len(names) > runSegmentsMax {
			return manifest{}, ErrManifest
		}
		man.runs = append(man.runs, names)
	}
	return man, nil
}

func (db *DB) manifestText() string {
	var sb strings.Builder
	db.mu.Lock()
	sb.WriteString(manifestHeader + "\nseq " + strconv.FormatUint(db.seq, 10))
	for _, r := range db.current.Load().runs {
		sb.WriteString("\n" + strings.TrimSuffix(runPrefix, " "))
		for _, s := range r.segs {
			sb.WriteString(" " + s.name)
		}
	}
	db.mu.Unlock()
	body := sb.String()
	return body + "\ncrc " + strconv.FormatUint(uint64(crc32.Checksum([]byte(body), castagnoli)), 10) + "\n"
}

func (db *DB) writeManifest() error {
	db.manifest.Lock()
	defer db.manifest.Unlock()
	text := db.manifestText()
	file, err := db.fs.Create(manifestTemp)
	if err != nil {
		return err
	}
	_, err = file.Write([]byte(text))
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	return db.fs.Rename(goio.Move{From: manifestTemp, To: manifestName})
}
