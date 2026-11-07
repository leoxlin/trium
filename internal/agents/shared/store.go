package shared

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/leoxlin/skillets/internal/resource"
)

type ConflictCheck func(context.Context, resource.Skill, string) (string, error)

type Store struct {
	Root       string
	BackupRoot string
	Check      ConflictCheck
}

type backupEntry struct {
	Name    string `json:"name"`
	Target  string `json:"target"`
	Backup  string `json:"backup,omitempty"`
	Existed bool   `json:"existed"`
}

type mutation struct {
	backupEntry
}

func (s *Store) Plan(ctx context.Context, desired []resource.Skill) (resource.ChangeSet[resource.Skill], error) {
	if len(desired) == 0 {
		return resource.ChangeSet[resource.Skill]{}, nil
	}
	current := make(map[string]resource.Skill, len(desired))
	conflicts := make(map[string]string, len(desired))
	targets := make([]resource.TargetDigest, 0, len(desired))
	for _, wanted := range desired {
		if err := ctx.Err(); err != nil {
			return resource.ChangeSet[resource.Skill]{}, err
		}
		path := filepath.Join(s.Root, wanted.Name)
		existing, target, reason, err := scan(path, wanted.Name)
		if err != nil {
			return resource.ChangeSet[resource.Skill]{}, err
		}
		targets = append(targets, target)
		if existing != nil {
			current[wanted.Name] = *existing
		}
		if reason == "" && s.Check != nil {
			reason, err = s.Check(ctx, wanted, path)
			if err != nil {
				return resource.ChangeSet[resource.Skill]{}, err
			}
		}
		if reason != "" {
			conflicts[wanted.Name] = reason
		}
	}
	changes, err := resource.Diff(desired, current)
	if err != nil {
		return resource.ChangeSet[resource.Skill]{}, err
	}
	for i := range changes {
		if reason := conflicts[changes[i].Desired.Name]; reason != "" {
			changes[i].Action = resource.Conflict
			changes[i].Reason = reason
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Path < targets[j].Path })
	return resource.ChangeSet[resource.Skill]{Changes: changes, Targets: targets}, nil
}

func (s *Store) Apply(ctx context.Context, changes resource.ChangeSet[resource.Skill]) (resource.ApplyReport, error) {
	var report resource.ApplyReport
	if changes.HasConflicts() {
		return report, errors.New("cannot apply a change set with conflicts")
	}
	if !changes.HasChanges() {
		return report, nil
	}
	for _, target := range changes.Targets {
		_, current, _, err := scan(target.Path, filepath.Base(target.Path))
		if err != nil {
			var unsafe *resource.UnsafePathError
			if errors.As(err, &unsafe) {
				return report, &resource.DriftError{Path: target.Path}
			}
			return report, err
		}
		if current.Exists != target.Exists || current.Digest != target.Digest {
			return report, &resource.DriftError{Path: target.Path}
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := os.MkdirAll(s.BackupRoot, 0o700); err != nil {
		return report, fmt.Errorf("create backup root: %w", err)
	}
	runDir, err := os.MkdirTemp(s.BackupRoot, "run-")
	if err != nil {
		return report, fmt.Errorf("create backup directory: %w", err)
	}
	report.Backups = []string{runDir}

	var mutations []mutation
	for _, change := range changes.Changes {
		if change.Action != resource.Add && change.Action != resource.Update {
			continue
		}
		entry := backupEntry{
			Name:    change.Desired.Name,
			Target:  filepath.Join(s.Root, change.Desired.Name),
			Existed: change.Current != nil,
		}
		if entry.Existed {
			entry.Backup = filepath.Join(runDir, "skills", change.Desired.Name)
		}
		mutations = append(mutations, mutation{backupEntry: entry})
	}
	if err := writeMetadata(runDir, mutations); err != nil {
		return report, err
	}

	var applied []mutation
	fail := func(applyErr error) (resource.ApplyReport, error) {
		restoreErr := restoreAll(applied)
		if restoreErr != nil {
			report.Warnings = append(report.Warnings, "restoration incomplete: "+restoreErr.Error())
		} else if len(applied) > 0 {
			report.Warnings = append(report.Warnings, "skill changes restored")
		}
		return report, applyErr
	}

	for i := range mutations {
		m := &mutations[i]
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		wanted := findDesired(changes.Changes, m.Name)
		if err := os.MkdirAll(s.Root, 0o755); err != nil {
			return fail(fmt.Errorf("create skill root: %w", err))
		}
		stage, err := os.MkdirTemp(s.Root, ".skillet-stage-")
		if err != nil {
			return fail(fmt.Errorf("stage skill %s: %w", m.Name, err))
		}
		if err := writeBundle(stage, wanted.Files); err != nil {
			_ = os.RemoveAll(stage)
			return fail(fmt.Errorf("stage skill %s: %w", m.Name, err))
		}
		if m.Existed {
			if err := os.MkdirAll(filepath.Dir(m.Backup), 0o700); err != nil {
				_ = os.RemoveAll(stage)
				return fail(fmt.Errorf("prepare skill backup: %w", err))
			}
			if err := os.Rename(m.Target, m.Backup); err != nil {
				_ = os.RemoveAll(stage)
				return fail(fmt.Errorf("back up skill %s: %w", m.Name, err))
			}
		}
		if err := os.Rename(stage, m.Target); err != nil {
			_ = os.RemoveAll(stage)
			if m.Existed {
				_ = os.Rename(m.Backup, m.Target)
			}
			return fail(fmt.Errorf("install skill %s: %w", m.Name, err))
		}
		applied = append(applied, *m)
	}

	desired := make([]resource.Skill, 0, len(mutations))
	for _, m := range mutations {
		desired = append(desired, findDesired(changes.Changes, m.Name))
	}
	verification, err := s.Plan(ctx, desired)
	if err != nil {
		return fail(fmt.Errorf("verify installed skills: %w", err))
	}
	for _, change := range verification.Changes {
		if change.Action != resource.Unchanged {
			return fail(fmt.Errorf("verify installed skill %s: %s", change.Desired.Name, change.Reason))
		}
	}
	for _, change := range changes.Changes {
		if change.Action == resource.Add || change.Action == resource.Update {
			report.Applied = append(report.Applied, resource.AppliedItem{Key: change.Desired.Key(), Action: change.Action})
		}
	}
	return report, nil
}

func scan(path, name string) (*resource.Skill, resource.TargetDigest, string, error) {
	files, err := resource.Snapshot(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, resource.TargetDigest{Path: path}, "", nil
	}
	if err != nil {
		var unsafe *resource.UnsafePathError
		if errors.As(err, &unsafe) {
			info, lstatErr := os.Lstat(path)
			if lstatErr != nil {
				return nil, resource.TargetDigest{}, "", lstatErr
			}
			digest := sha256.Sum256([]byte(info.Mode().String()))
			return nil, resource.TargetDigest{Path: path, Exists: true, Digest: digest}, "skill target must be a real directory without links or special files", nil
		}
		return nil, resource.TargetDigest{}, "", fmt.Errorf("scan skill %s: %w", path, err)
	}
	skill := resource.NewSkill(name, path, files)
	return &skill, resource.TargetDigest{Path: path, Exists: true, Digest: skill.Fingerprint()}, "", nil
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

func findDesired(changes []resource.Change[resource.Skill], name string) resource.Skill {
	for _, change := range changes {
		if change.Desired.Name == name {
			return change.Desired
		}
	}
	return resource.Skill{}
}
