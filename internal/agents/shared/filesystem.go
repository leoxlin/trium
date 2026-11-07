package shared

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/leoxlin/skillets/internal/resource"
)

type backupEntry struct {
	Name    string `json:"name"`
	Target  string `json:"target"`
	Backup  string `json:"backup,omitempty"`
	Existed bool   `json:"existed"`
}

type mutation struct {
	backupEntry
}

func writeBundle(root string, files []resource.File) error {
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if file.Executable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Data, mode); err != nil {
			return err
		}
	}
	return nil
}

func writeMetadata(runDir string, mutations []mutation) error {
	entries := make([]backupEntry, len(mutations))
	for i, m := range mutations {
		entries[i] = m.backupEntry
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(runDir, "metadata.json"), data, 0o600); err != nil {
		return fmt.Errorf("write backup metadata: %w", err)
	}
	return nil
}

func restoreAll(mutations []mutation) error {
	var errs []error
	for i := len(mutations) - 1; i >= 0; i-- {
		m := mutations[i]
		if err := os.RemoveAll(m.Target); err != nil {
			errs = append(errs, err)
			continue
		}
		if m.Existed {
			if err := os.Rename(m.Backup, m.Target); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// CheckPath rejects symlinks in all existing target ancestors, including missing children.
func CheckPath(path string) (string, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "target must not traverse symlinks", nil
		}
		if filepath.Dir(current) == current {
			return "", nil
		}
	}
}

func ReadTarget(path string) ([]byte, resource.TargetDigest, error) {
	target := resource.TargetDigest{Path: path}
	reason, err := CheckPath(path)
	if err != nil {
		return nil, target, err
	}
	if reason != "" {
		return nil, target, &resource.UnsafePathError{Path: path, Reason: reason}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, target, nil
	}
	if err != nil {
		return nil, target, err
	}
	if !info.Mode().IsRegular() {
		return nil, target, &resource.UnsafePathError{Path: path, Reason: "registry must be a regular file"}
	}
	data, err := os.ReadFile(path)
	target.Exists, target.Digest = true, sha256.Sum256(data)
	return data, target, err
}

type Write struct {
	Path      string
	Files     []resource.File
	Data      []byte
	Directory bool
}

func digestTarget(target resource.TargetDigest, directory bool) (resource.TargetDigest, error) {
	if !directory {
		_, current, err := ReadTarget(target.Path)
		return current, err
	}
	reason, err := CheckPath(target.Path)
	if err != nil {
		return resource.TargetDigest{}, err
	}
	if reason != "" {
		return resource.TargetDigest{}, &resource.UnsafePathError{Path: target.Path, Reason: reason}
	}
	files, err := resource.Snapshot(target.Path)
	if errors.Is(err, os.ErrNotExist) {
		return resource.TargetDigest{Path: target.Path}, nil
	}
	return resource.TargetDigest{Path: target.Path, Exists: true, Digest: resource.HashFiles(files)}, err
}

// applyWrites owns staging, backups, verification and rollback for one resource class.
func applyWrites(ctx context.Context, backupRoot string, writes []Write, verify func() error) (resource.ApplyReport, error) {
	var report resource.ApplyReport
	if err := ctx.Err(); err != nil {
		return report, err
	}
	reason, err := CheckPath(backupRoot)
	if err != nil {
		return report, err
	}
	if reason != "" {
		return report, &resource.UnsafePathError{Path: backupRoot, Reason: reason}
	}
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return report, err
	}
	run, err := os.MkdirTemp(backupRoot, "run-")
	if err != nil {
		return report, err
	}
	report.Backups = []string{run}
	var mutations []mutation
	for i, w := range writes {
		_, err := os.Lstat(w.Path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return report, err
		}
		mutations = append(mutations, mutation{backupEntry: backupEntry{Name: filepath.Base(w.Path), Target: w.Path, Backup: filepath.Join(run, fmt.Sprint(i)), Existed: err == nil}})
	}
	if err := writeMetadata(run, mutations); err != nil {
		return report, err
	}
	var applied []mutation
	fail := func(err error) (resource.ApplyReport, error) {
		if restoreErr := restoreAll(applied); restoreErr != nil {
			report.Warnings = append(report.Warnings, "restoration incomplete: "+restoreErr.Error())
		}
		return report, err
	}
	for i, w := range writes {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(filepath.Dir(w.Path), 0o755); err != nil {
			return fail(err)
		}
		stage, err := os.MkdirTemp(filepath.Dir(w.Path), ".skillet-stage-")
		if err != nil {
			return fail(err)
		}
		staged := stage
		if w.Directory {
			err = writeBundle(stage, w.Files)
		} else {
			staged = filepath.Join(stage, "file")
			mode := os.FileMode(0o600)
			if info, statErr := os.Stat(w.Path); statErr == nil {
				mode = info.Mode().Perm()
			}
			err = os.WriteFile(staged, w.Data, mode)
		}
		if err != nil {
			_ = os.RemoveAll(stage)
			return fail(err)
		}
		m := mutations[i]
		if m.Existed {
			if err = os.Rename(m.Target, m.Backup); err != nil {
				_ = os.RemoveAll(stage)
				return fail(err)
			}
		}
		applied = append(applied, m)
		err = os.Rename(staged, w.Path)
		if !w.Directory || err != nil {
			_ = os.RemoveAll(stage)
		}
		if err != nil {
			return fail(err)
		}
	}
	if err := verify(); err != nil {
		return fail(err)
	}
	return report, nil
}
func scanBundle(path string) ([]resource.File, resource.TargetDigest, string, error) {
	reason, err := CheckPath(path)
	if err != nil {
		return nil, resource.TargetDigest{}, "", err
	}
	if reason != "" {
		return nil, resource.TargetDigest{Path: path}, reason, nil
	}
	target := resource.TargetDigest{Path: path}
	files, err := resource.Snapshot(path)
	if err != nil {
		var unsafe *resource.UnsafePathError
		if errors.As(err, &unsafe) {
			return nil, target, unsafe.Reason, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, target, "", nil
		}
		return nil, target, "", err
	}
	target.Exists, target.Digest = true, resource.HashFiles(files)
	return files, target, "", nil
}

// ReadJSON preserves unknown native fields and rejects non-object registries.
func ReadJSON(path string, initial map[string]any) (map[string]any, resource.TargetDigest, error) {
	data, target, err := ReadTarget(path)
	if err != nil {
		return nil, target, err
	}
	if !target.Exists {
		return initial, target, nil
	}
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, target, fmt.Errorf("decode %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, target, fmt.Errorf("%s must contain one JSON object", path)
	}
	if object == nil {
		return nil, target, fmt.Errorf("%s must contain an object", path)
	}
	return object, target, nil
}

func JSONWrite(path string, value any) (Write, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return Write{Path: path, Data: append(data, '\n')}, err
}

// ObjectField creates a missing object but rejects incompatible native shapes.
func ObjectField(object map[string]any, key string) (map[string]any, error) {
	value, exists := object[key]
	if !exists {
		value = map[string]any{}
		object[key] = value
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return result, nil
}
