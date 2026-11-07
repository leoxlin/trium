package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/leoxlin/skillets/internal/resource"
)

// SnapshotGit acquires a validated repository ref and bundle path relative to workingDir.
// It returns the resolved source and files, removing its temporary repository before returning.
func SnapshotGit(ctx context.Context, workingDir, repositoryURL, ref, bundlePath string) (string, []resource.File, error) {
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
			return "", nil, &resource.UnsafePathError{Path: bundlePath, Reason: "skill bundles cannot contain submodules"}
		}
	}
	archive, err := run("--git-dir="+repository, "archive", "--format=tar", tree)
	if err != nil {
		return "", nil, err
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	var files []resource.File
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("read Git archive: %w", err)
		}
		if !filepath.IsLocal(header.Name) || strings.Contains(header.Name, `\`) {
			return "", nil, &resource.UnsafePathError{Path: header.Name, Reason: "unsafe archive path"}
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return "", nil, &resource.UnsafePathError{Path: header.Name, Reason: "skill bundles cannot contain links or special files"}
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return "", nil, fmt.Errorf("read Git file: %w", err)
		}
		files = append(files, resource.File{Path: header.Name, Data: data, Executable: header.Mode&0o111 != 0})
	}
	source := repositoryURL + "@" + sha + ":" + bundlePath
	return source, files, nil
}
