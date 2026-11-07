package bundle

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// ReadDir reads a real directory without following symlinked ancestors or entries.
func ReadDir(ctx context.Context, root string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	for current := filepath.Clean(root); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return Snapshot{}, &UnsafePathError{Path: current, Reason: "bundle path must not traverse symlinks"}
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	files, err := readDir(ctx, root)
	if err != nil {
		return Snapshot{}, err
	}
	return newSnapshot(root, files), nil
}

func readDir(ctx context.Context, root string) ([]File, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, &UnsafePathError{Path: root, Reason: "bundle must be a real directory"}
	}

	var files []File
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
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
			return &UnsafePathError{Path: path, Reason: "bundles cannot contain links or special files"}
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
