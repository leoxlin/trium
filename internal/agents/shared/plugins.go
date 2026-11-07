package shared

import (
	"context"
	"errors"
	"fmt"
	"sort"

	nativeplugin "github.com/leoxlin/skillets/internal/plugin"
	"github.com/leoxlin/skillets/internal/resource"
)

type PluginInstall struct {
	Path    string
	Enabled bool
	Local   bool
}

type PluginRegistration struct {
	CopyPaths map[string][]string
	Installed map[string]PluginInstall
	Paths     map[string]string
	Targets   []resource.TargetDigest
	Writes    []Write
	Conflicts map[string]string
}

// PluginStore handles native plugin bundles and registrations together. Registry
// codecs remain in the agent packages and never receive standalone skills.
type PluginStore struct {
	Agent      string
	BackupRoot string
	Register   func([]resource.Plugin) (PluginRegistration, error)
}

func (s *PluginStore) Plan(ctx context.Context, desired []resource.Plugin) (resource.ChangeSet[resource.Plugin], error) {
	var plan resource.ChangeSet[resource.Plugin]
	if len(desired) == 0 {
		return plan, nil
	}
	registration, err := s.Register(desired)
	if err != nil {
		return plan, err
	}
	plan.Targets = append(plan.Targets, registration.Targets...)
	current := map[string]resource.Plugin{}
	needsUpdate := map[string]bool{}
	conflicts := registration.Conflicts
	if conflicts == nil {
		conflicts = map[string]string{}
	}
	for _, wanted := range desired {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		if err := nativeplugin.Validate(wanted.Name, s.Agent, wanted.Files); err != nil {
			return plan, err
		}
		installed, registered := registration.Installed[wanted.Name]
		path := registration.Paths[wanted.Name]
		if registered && !installed.Local {
			conflicts[wanted.Name] = "plugin is managed by another source"
			continue
		}
		if registered {
			path = installed.Path
		}
		for _, copyPath := range registration.CopyPaths[wanted.Name] {
			files, target, reason, err := scanBundle(copyPath)
			if err != nil {
				return plan, err
			}
			plan.Targets = append(plan.Targets, target)
			if reason != "" {
				conflicts[wanted.Name] = reason
			}
			if !target.Exists || resource.HashFiles(files) != wanted.Hash {
				needsUpdate[wanted.Name] = true
			}
		}
		files, target, reason, err := scanBundle(path)
		if err != nil {
			return plan, err
		}
		plan.Targets = append(plan.Targets, target)
		switch {
		case reason != "":
			conflicts[wanted.Name] = reason
		case registered && !target.Exists:
			conflicts[wanted.Name] = "plugin registration has no bundle"
		case !registered && target.Exists:
			conflicts[wanted.Name] = "plugin bundle has no registration"
		case registered:
			existing := resource.NewPlugin(wanted.Name, path, files)
			existing.Enabled = installed.Enabled
			current[wanted.Name] = existing
		}

	}
	changes, err := resource.Diff(desired, current)
	if err != nil {
		return plan, err
	}
	for i := range changes {
		if changes[i].Action == resource.Unchanged && needsUpdate[changes[i].Desired.Name] {
			changes[i].Action = resource.Update
			changes[i].Reason = "native plugin cache differs"
		}
		if reason := conflicts[changes[i].Desired.Name]; reason != "" {
			changes[i].Action = resource.Conflict
			changes[i].Reason = reason
		} else if changes[i].Action == resource.Add || changes[i].Action == resource.Update {
			changes[i].Reason += "; enables plugin code in future agent sessions"
		}
	}
	plan.Changes = changes
	sort.Slice(plan.Targets, func(i, j int) bool { return plan.Targets[i].Path < plan.Targets[j].Path })
	return plan, nil
}

func (s *PluginStore) Apply(ctx context.Context, changes resource.ChangeSet[resource.Plugin]) (resource.ApplyReport, error) {
	var report resource.ApplyReport
	if changes.HasConflicts() {
		return report, errors.New("cannot apply plugin conflicts")
	}
	if !changes.HasChanges() {
		return report, nil
	}
	var desired []resource.Plugin
	for _, change := range changes.Changes {
		desired = append(desired, change.Desired)
	}
	registration, err := s.Register(desired)
	if err != nil {
		return report, err
	}
	registryPaths := map[string]bool{}
	for _, target := range registration.Targets {
		registryPaths[target.Path] = true
	}
	for _, target := range changes.Targets {
		current, err := digestTarget(target, !registryPaths[target.Path])
		if err != nil || current != target {
			return report, &resource.DriftError{Path: target.Path}
		}
	}
	var writes []Write
	for _, change := range changes.Changes {
		if change.Action == resource.Add || change.Action == resource.Update {
			writes = append(writes, Write{Path: registration.Paths[change.Desired.Name], Files: change.Desired.Files, Directory: true})
			for _, path := range registration.CopyPaths[change.Desired.Name] {
				writes = append(writes, Write{Path: path, Files: change.Desired.Files, Directory: true})
			}
		}
	}
	writes = append(writes, registration.Writes...)
	report, err = applyWrites(ctx, s.BackupRoot, writes, func() error {
		verification, err := s.Plan(ctx, desired)
		if err != nil {
			return err
		}
		for _, change := range verification.Changes {
			if change.Action != resource.Unchanged {
				return fmt.Errorf("verify plugin %s: %s", change.Desired.Name, change.Reason)
			}
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	for _, change := range changes.Changes {
		if change.Action == resource.Add || change.Action == resource.Update {
			report.Applied = append(report.Applied, resource.AppliedItem{Key: change.Desired.Name, Kind: "plugins", Action: change.Action})
		}
	}
	return report, nil
}
