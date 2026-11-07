package resource

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

// SnapshotGit acquires a validated repository ref and bundle path relative to workingDir.
// It returns the resolved source and files, removing its temporary repository before returning.
func SnapshotGit(ctx context.Context, workingDir, repositoryURL, ref, bundlePath string) (string, []File, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	temporary, err := os.MkdirTemp("", "skillet-git-*")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(temporary)
	repository := filepath.Join(temporary, "repo.git")
	run := func(args ...string) ([]byte, error) {
		args = append([]string{"-c", "core.hooksPath=" + os.DevNull}, args...)
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = workingDir
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("git: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return output, nil
	}
	if _, err := run("init", "--bare", "--quiet", repository); err != nil {
		return "", nil, err
	}
	if _, err := run("--git-dir="+repository, "fetch", "--quiet", "--depth=1", "--", repositoryURL, ref); err != nil {
		return "", nil, err
	}
	commit, err := run("--git-dir="+repository, "rev-parse", "FETCH_HEAD^{commit}")
	if err != nil {
		return "", nil, err
	}
	sha := strings.TrimSpace(string(commit))
	tree := sha + ":"
	if bundlePath != "." {
		tree += bundlePath
	}
	entries, err := run("--git-dir="+repository, "ls-tree", "-r", "-z", tree)
	if err != nil {
		return "", nil, err
	}
	for _, entry := range bytes.Split(entries, []byte{0}) {
		if bytes.HasPrefix(entry, []byte("160000 ")) {
			return "", nil, &UnsafePathError{Path: bundlePath, Reason: "skill bundles cannot contain submodules"}
		}
	}
	archive, err := run("--git-dir="+repository, "archive", "--format=tar", tree)
	if err != nil {
		return "", nil, err
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	var files []File
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("read Git archive: %w", err)
		}
		if !filepath.IsLocal(header.Name) || strings.Contains(header.Name, `\`) {
			return "", nil, &UnsafePathError{Path: header.Name, Reason: "unsafe archive path"}
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return "", nil, &UnsafePathError{Path: header.Name, Reason: "skill bundles cannot contain links or special files"}
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return "", nil, fmt.Errorf("read Git file: %w", err)
		}
		files = append(files, File{Path: header.Name, Data: data, Executable: header.Mode&0o111 != 0})
	}
	source := repositoryURL + "@" + sha + ":" + bundlePath
	return source, files, nil
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
