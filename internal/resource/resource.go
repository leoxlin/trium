package resource

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Digest [32]byte

func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

type Item interface {
	Key() string
	Fingerprint() Digest
}

type File struct {
	Path       string
	Data       []byte
	Executable bool
}

type Skill struct {
	Name   string
	Source string
	Files  []File
	Hash   Digest
}

func NewSkill(name, source string, files []File) Skill {
	files = cloneFiles(files)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return Skill{Name: name, Source: source, Files: files, Hash: HashFiles(files)}
}

func (s Skill) Key() string {
	return s.Name
}

func (s Skill) Fingerprint() Digest {
	return s.Hash
}

func HashFiles(files []File) Digest {
	h := sha256.New()
	h.Write([]byte("skillet-skill-v1\x00"))
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

func Snapshot(root string) ([]File, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, &UnsafePathError{Path: root, Reason: "skill bundle must be a real directory"}
	}

	var files []File
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return &UnsafePathError{Path: path, Reason: "skill bundles cannot contain links or special files"}
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, File{
			Path:       filepath.ToSlash(relative),
			Data:       data,
			Executable: info.Mode().Perm()&0o111 != 0,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

type UnsafePathError struct {
	Path   string
	Reason string
}

func (e *UnsafePathError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Reason)
}

func cloneFiles(files []File) []File {
	cloned := make([]File, len(files))
	for i, file := range files {
		cloned[i] = file
		cloned[i].Data = append([]byte(nil), file.Data...)
	}
	return cloned
}

func FindFile(files []File, path string) (File, bool) {
	for _, file := range files {
		if file.Path == strings.TrimPrefix(filepath.ToSlash(path), "./") {
			return file, true
		}
	}
	return File{}, false
}
