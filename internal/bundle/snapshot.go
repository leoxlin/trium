package bundle

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
)

type File struct {
	Path       string
	Data       []byte
	Executable bool
}

type Digest [32]byte

func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// Snapshot owns a sorted, immutable set of regular files.
type Snapshot struct {
	files  []File
	digest Digest
	source string
}

func (s Snapshot) Files() []File  { return cloneFiles(s.files) }
func (s Snapshot) Digest() Digest { return s.digest }
func (s Snapshot) Source() string { return s.source }

func newSnapshot(source string, files []File) Snapshot {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return Snapshot{source: source, files: files, digest: hashFiles(files)}
}

// UnsafePathError identifies content or paths that cannot be safely read.
type UnsafePathError struct{ Path, Reason string }

func (e *UnsafePathError) Error() string { return fmt.Sprintf("%s: %s", e.Path, e.Reason) }

func hashFiles(files []File) Digest {
	h := sha256.New()
	h.Write([]byte("trium-bundle-v1\x00"))
	for _, file := range files {
		var pathLength [4]byte
		binary.BigEndian.PutUint32(pathLength[:], uint32(len(file.Path)))
		h.Write(pathLength[:])
		h.Write([]byte(file.Path))
		if file.Executable {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
		var dataLength [8]byte
		binary.BigEndian.PutUint64(dataLength[:], uint64(len(file.Data)))
		h.Write(dataLength[:])
		h.Write(file.Data)
	}
	var digest Digest
	copy(digest[:], h.Sum(nil))
	return digest
}

func cloneFiles(files []File) []File {
	cloned := make([]File, len(files))
	for i, file := range files {
		cloned[i] = file
		cloned[i].Data = append([]byte(nil), file.Data...)
	}
	return cloned
}
