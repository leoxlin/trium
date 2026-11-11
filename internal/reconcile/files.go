package reconcile

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
	"strings"

	"github.com/leoxlin/trium/internal/bundle"
)

// checkPath rejects symlinks in all existing target ancestors, including missing children.
func checkPath(path string) (string, error) {
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

func readTarget(path string) ([]byte, observation, error) {
	target := observation{Path: path}
	reason, err := checkPath(path)
	if err != nil {
		return nil, target, err
	}
	if reason != "" {
		return nil, target, &bundle.UnsafePathError{Path: path, Reason: reason}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, target, nil
	}
	if err != nil {
		return nil, target, err
	}
	if !info.Mode().IsRegular() {
		return nil, target, &bundle.UnsafePathError{Path: path, Reason: "registry must be a regular file"}
	}
	data, err := os.ReadFile(path)
	target.Exists, target.Digest = true, sha256.Sum256(data)
	return data, target, err
}

// readJSON preserves unknown native fields and rejects non-object registries.
func readJSON(path string, initial map[string]any) (map[string]any, observation, error) {
	data, target, err := readTarget(path)
	if err != nil {
		return nil, target, err
	}
	if !target.Exists {
		return initial, target, nil
	}
	object, err := decodeObject(data)
	if err != nil {
		return nil, target, fmt.Errorf("decode %s: %w", path, err)
	}
	return object, target, nil
}

func decodeObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("must contain one JSON object")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("must contain a JSON object")
	}
	return object, nil
}

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := map[string]any{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("JSON object key must be a string")
			}
			if _, exists := object[name]; exists {
				return nil, fmt.Errorf("duplicate JSON key %q", name)
			}
			object[name], err = decodeValue(decoder)
			if err != nil {
				return nil, err
			}
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		values := []any{}
		for decoder.More() {
			value, err := decodeValue(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err := decoder.Token()
		return values, err
	default:
		return token, nil
	}
}

func jsonWrite(path string, value any) (replacement, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return replacement{Path: path, Data: append(data, '\n')}, err
}

// objectField creates a missing object but rejects incompatible native shapes.
func objectField(object map[string]any, key string) (map[string]any, error) {
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

func scanBundle(ctx context.Context, path string) (bundle.Snapshot, observation, string, error) {
	target := observation{Path: path, Directory: true}
	reason, err := checkPath(path)
	if err != nil || reason != "" {
		return bundle.Snapshot{}, target, reason, err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return bundle.Snapshot{}, target, "", nil
	} else if err != nil {
		return bundle.Snapshot{}, target, "", err
	}
	snapshot, err := bundle.ReadDir(ctx, path)
	if err != nil {
		var unsafe *bundle.UnsafePathError
		if errors.As(err, &unsafe) {
			return bundle.Snapshot{}, target, unsafe.Reason, nil
		}
		return bundle.Snapshot{}, target, "", err
	}
	target.Exists, target.Digest = true, snapshot.Digest()
	return snapshot, target, "", nil
}

func checkReads(ctx context.Context, reads []observation) error {
	for _, expected := range reads {
		if err := ctx.Err(); err != nil {
			return err
		}
		var current observation
		var err error
		if expected.Directory {
			var reason string
			_, current, reason, err = scanBundle(ctx, expected.Path)
			if reason != "" {
				return &DriftError{Path: expected.Path}
			}
		} else {
			_, current, err = readTarget(expected.Path)
		}
		if err != nil || current != expected {
			return &DriftError{Path: expected.Path}
		}
	}
	return nil
}

func writeBundle(root string, files []bundle.File) error {
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if !filepath.IsLocal(file.Path) {
			return fmt.Errorf("unsafe bundle path %q", file.Path)
		}
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

type mutation struct {
	Target  string `json:"target"`
	Backup  string `json:"backup,omitempty"`
	Existed bool   `json:"existed"`
	stage   string
	keep    bool
}

// applyBatch owns staging, backups, verification, rollback, and stage cleanup.
func applyBatch(ctx context.Context, backupRoot string, batch writeBatch, verify func(context.Context) error) (report batchReport, err error) {
	if len(batch.writes) == 0 {
		return report, nil
	}
	if err := checkReads(ctx, batch.reads); err != nil {
		return report, err
	}
	for i, write := range batch.writes {
		if !filepath.IsAbs(write.Path) {
			return report, fmt.Errorf("target must be absolute: %s", write.Path)
		}
		reason, err := checkPath(write.Path)
		if err != nil {
			return report, err
		}
		if reason != "" {
			return report, &bundle.UnsafePathError{Path: write.Path, Reason: reason}
		}
		observed := false
		for _, read := range batch.reads {
			if read.Path == write.Path && read.Directory == write.Directory {
				observed = true
			}
		}
		if !observed {
			return report, fmt.Errorf("write target was not observed: %s", write.Path)
		}
		// ponytail: pairwise overlap checks; sort paths if manifests grow large.
		for _, other := range batch.writes[:i] {
			if overlaps(write.Path, other.Path) {
				return report, fmt.Errorf("overlapping writes: %s and %s", write.Path, other.Path)
			}
		}
		if overlaps(write.Path, backupRoot) {
			return report, fmt.Errorf("backup path overlaps target: %s", write.Path)
		}
	}
	reason, err := checkPath(backupRoot)
	if err != nil {
		return report, err
	}
	if reason != "" {
		return report, &bundle.UnsafePathError{Path: backupRoot, Reason: reason}
	}
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return report, err
	}
	run, err := os.MkdirTemp(backupRoot, "run-")
	if err != nil {
		return report, err
	}
	report.Backups = []string{run}
	var mutations []*mutation
	defer func() {
		for _, m := range mutations {
			if !m.keep {
				if err := os.RemoveAll(m.stage); err != nil {
					report.Warnings = append(report.Warnings, fmt.Sprintf("stage cleanup failed for %s: %v", m.stage, err))
				}
			}
		}
	}()
	// All replacements and original backups are ready before the first rename.
	for i, write := range batch.writes {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if err := os.MkdirAll(filepath.Dir(write.Path), 0o755); err != nil {
			return report, err
		}
		stage, err := os.MkdirTemp(filepath.Dir(write.Path), ".trium-stage-")
		if err != nil {
			return report, err
		}
		m := &mutation{Target: write.Path, Backup: filepath.Join(run, fmt.Sprint(i)), stage: stage}
		mutations = append(mutations, m)
		info, err := os.Lstat(write.Path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return report, err
		}
		m.Existed = err == nil
		staged := filepath.Join(stage, "new")
		if write.Directory {
			mode := os.FileMode(0o755)
			if m.Existed {
				mode = info.Mode().Perm()
			}
			if err := os.Mkdir(staged, 0o700); err != nil {
				return report, err
			}
			if err := writeBundle(staged, write.Files); err != nil {
				return report, err
			}
			if err := os.Chmod(staged, mode); err != nil {
				return report, err
			}
		} else {
			mode := os.FileMode(0o600)
			if m.Existed {
				mode = info.Mode().Perm()
			}
			if err := os.WriteFile(staged, write.Data, mode); err != nil {
				return report, err
			}
		}
		if m.Existed {
			if err := copyTarget(ctx, m.Target, m.Backup); err != nil {
				return report, fmt.Errorf("backup %s: %w", m.Target, err)
			}
		}
	}
	metadata, err := json.MarshalIndent(mutations, "", "  ")
	if err != nil {
		return report, err
	}
	if err := os.WriteFile(filepath.Join(run, "metadata.json"), append(metadata, '\n'), 0o600); err != nil {
		return report, err
	}
	if err := checkReads(ctx, batch.reads); err != nil {
		return report, err
	}
	var applied []*mutation
	fail := func(cause error) (batchReport, error) {
		for i := len(applied) - 1; i >= 0; i-- {
			m := applied[i]
			err := os.RemoveAll(m.Target)
			if err == nil && m.Existed {
				err = os.Rename(filepath.Join(m.stage, "old"), m.Target)
			}
			if err != nil {
				m.keep = true
				report.Warnings = append(report.Warnings, fmt.Sprintf("restoration incomplete for %s; recovery files in %s: %v", m.Target, m.stage, err))
			}
		}
		return report, cause
	}
	for _, m := range mutations {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if m.Existed {
			if err := os.Rename(m.Target, filepath.Join(m.stage, "old")); err != nil {
				return fail(err)
			}
		}
		applied = append(applied, m)
		if err := os.Rename(filepath.Join(m.stage, "new"), m.Target); err != nil {
			return fail(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := verify(ctx); err != nil {
		return fail(err)
	}
	return report, nil
}

func overlaps(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

func copyTarget(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.Mkdir(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("backup target is not a regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
