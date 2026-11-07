package reconcile

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/leoxlin/skillets/internal/bundle"
	"github.com/leoxlin/skillets/internal/manifest"
)

type Homes struct{ User, Codex, Claude, Kimi string }
type native interface {
	validate(desired) error
	plan(context.Context, desired) (targetPlan, error)
}
type selectedTarget struct {
	agent, home string
	native      native
}
type Prepared struct {
	wanted   desired
	targets  []selectedTarget
	lockPath string
}

// Prepare acquires sources once and validates every selected agent before target I/O.
func Prepare(ctx context.Context, spec manifest.Manifest, baseDir string, homes Homes) (*Prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(baseDir) {
		return nil, errors.New("manifest directory must be absolute")
	}
	for _, path := range []string{homes.User, homes.Codex, homes.Claude, homes.Kimi} {
		if !filepath.IsAbs(path) {
			return nil, errors.New("all homes must be absolute")
		}
	}
	if len(spec.Targets) == 0 {
		return nil, &InputError{Field: "target", Err: errors.New("at least one target is required")}
	}
	p := &Prepared{lockPath: filepath.Join(homes.User, ".skillet", "apply.lock")}
	seenTargets := map[string]bool{}
	for i, target := range spec.Targets {
		home := target.Path
		if strings.HasPrefix(home, "~/") {
			home = filepath.Join(homes.User, strings.TrimPrefix(home, "~/"))
		}
		if home == "" {
			switch target.Agent {
			case "codex":
				home = homes.Codex
			case "claude":
				home = homes.Claude
			case "kimi":
				home = homes.Kimi
			}
		}
		if !filepath.IsAbs(home) || strings.ContainsFunc(home, unicode.IsControl) {
			return nil, &InputError{Field: fmt.Sprintf("target[%d].path", i), Err: errors.New("must be absolute or start with ~/")}
		}
		home = filepath.Clean(home)
		selected := selectedTarget{agent: target.Agent, home: home}
		switch target.Agent {
		case "codex":
			selected.native = &codexNative{home: home, skillsRoot: filepath.Join(homes.User, ".agents", "skills")}
		case "claude":
			selected.native = &claudeNative{home: home, skillsRoot: filepath.Join(home, "skills")}
		case "kimi":
			selected.native = &kimiNative{home: home, skillsRoot: filepath.Join(home, "skills")}
		default:
			return nil, &InputError{Field: fmt.Sprintf("target[%d].agent", i), Err: fmt.Errorf("unsupported agent %q", target.Agent)}
		}
		key := target.Agent + ":" + home
		if seenTargets[key] {
			return nil, &InputError{Field: fmt.Sprintf("target[%d]", i), Err: errors.New("duplicate resolved target")}
		}
		seenTargets[key] = true
		p.targets = append(p.targets, selected)
	}
	for _, group := range []struct {
		kind         Kind
		declarations []manifest.Declaration
		output       *[]namedBundle
	}{
		{Skills, spec.Skills, &p.wanted.skills}, {Plugins, spec.Plugins, &p.wanted.plugins},
	} {
		seen := map[string]bool{}
		for i, declaration := range group.declarations {
			field := fmt.Sprintf("%s[%d]", group.kind, i)
			if !resourceName.MatchString(declaration.Name) || seen[declaration.Name] {
				return nil, &InputError{Field: field + ".name", Err: errors.New("invalid or duplicate resource name")}
			}
			seen[declaration.Name] = true
			content, err := bundle.Acquire(ctx, baseDir, declaration.Source)
			if err != nil {
				var unsafe *bundle.UnsafePathError
				var reference *bundle.ReferenceError
				if errors.As(err, &unsafe) {
					return nil, &InputError{Field: field + ".path", Err: err}
				}
				if errors.As(err, &reference) {
					return nil, &InputError{Field: field + "." + reference.Field, Err: err}
				}
				return nil, fmt.Errorf("%s.source: %w", field, err)
			}
			*group.output = append(*group.output, namedBundle{name: declaration.Name, field: field, content: content})
		}
		sort.Slice(*group.output, func(i, j int) bool { return (*group.output)[i].name < (*group.output)[j].name })
	}
	if err := validateSkills(p.wanted.skills); err != nil {
		return nil, err
	}
	for _, target := range p.targets {
		if err := target.native.validate(p.wanted); err != nil {
			return nil, fmt.Errorf("%s: %w", target.agent, err)
		}
	}
	return p, nil
}

var resourceName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func (p *Prepared) Plan(ctx context.Context) ([]TargetPlan, error) {
	var plans []TargetPlan
	for _, target := range p.targets {
		if err := ctx.Err(); err != nil {
			return plans, err
		}
		plan, err := target.native.plan(ctx, p.wanted)
		if err != nil {
			return plans, fmt.Errorf("plan %s (%s): %w", target.agent, target.home, err)
		}
		plans = append(plans, TargetPlan{Agent: target.agent, Home: target.home, Changes: plan.changes()})
	}
	return plans, nil
}

func (p *Prepared) Apply(ctx context.Context) (results []TargetResult, err error) {
	unlock, err := acquireLock(ctx, p.lockPath)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	for _, target := range p.targets {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		plan, err := target.native.plan(ctx, p.wanted)
		result := TargetResult{Plan: TargetPlan{Agent: target.agent, Home: target.home, Changes: plan.changes()}}
		results = append(results, result)
		if err != nil {
			return results, fmt.Errorf("plan %s (%s): %w", target.agent, target.home, err)
		}
		if result.Plan.HasConflicts() {
			return results, nil
		}
		for _, batch := range plan.batches {
			if len(batch.writes) == 0 {
				continue
			}
			report, err := applyBatch(ctx, filepath.Join(target.home, ".skillet", "backups"), batch, func(ctx context.Context) error {
				verified, err := target.native.plan(ctx, p.wanted)
				if err != nil {
					return err
				}
				for _, change := range verified.changes() {
					if change.Kind == batch.kind && change.Action != Unchanged {
						return fmt.Errorf("verify %s/%s: %s", change.Kind, change.Name, change.Reason)
					}
				}
				return nil
			})
			result.Backups = append(result.Backups, report.Backups...)
			result.Warnings = append(result.Warnings, report.Warnings...)
			if err == nil {
				for _, change := range batch.changes {
					if change.Action == Add || change.Action == Update {
						result.Applied = append(result.Applied, change)
					}
				}
			}
			results[len(results)-1] = result
			if err != nil {
				return results, fmt.Errorf("apply %s (%s) %s: %w", target.agent, target.home, batch.kind, err)
			}
		}
	}
	return results, nil
}

func planTarget(ctx context.Context, wanted desired, root string, reads []observation, check func(string) (string, error), register func([]namedBundle) (pluginRegistration, error)) (targetPlan, error) {
	skills, err := planSkills(ctx, wanted.skills, root, reads, check)
	if err != nil {
		return targetPlan{}, err
	}
	plugins, err := planPlugins(ctx, wanted.plugins, register)
	if err != nil {
		return targetPlan{}, err
	}
	return targetPlan{batches: []writeBatch{skills, plugins}}, nil
}
