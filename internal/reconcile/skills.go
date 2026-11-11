package reconcile

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/leoxlin/trium/internal/bundle"
	"gopkg.in/yaml.v3"
)

func findFile(content bundle.Snapshot, path string) (bundle.File, bool) {
	for _, file := range content.Files() {
		if file.Path == path {
			return file, true
		}
	}
	return bundle.File{}, false
}

func validateSkills(skills []namedBundle) error {
	for _, wanted := range skills {
		file, ok := findFile(wanted.content, "SKILL.md")
		if !ok {
			return &InputError{Field: wanted.field, Err: errors.New("bundle must contain SKILL.md")}
		}
		if err := validateFrontmatter(file.Data, wanted.name); err != nil {
			return &InputError{Field: wanted.field + "/SKILL.md", Err: err}
		}
	}
	return nil
}

func planSkills(ctx context.Context, desired []namedBundle, root string, reads []observation, check func(string) (string, error)) (writeBatch, error) {
	batch := writeBatch{kind: Skills, reads: reads}
	for _, wanted := range desired {
		if err := ctx.Err(); err != nil {
			return batch, err
		}
		path := filepath.Join(root, wanted.name)
		// Agent policy can forbid a path before it is inspected.
		reason := ""
		var err error
		if check != nil {
			reason, err = check(path)
		}
		if err != nil {
			return batch, err
		}
		var target observation
		if reason == "" {
			_, target, reason, err = scanBundle(ctx, path)
		}
		if err != nil {
			return batch, err
		}
		change := compare(Skills, wanted, target.Digest, target.Exists)
		if reason != "" {
			change.Action, change.Reason = Conflict, reason
		} else {
			batch.reads = append(batch.reads, target)
			if change.Action == Add || change.Action == Update {
				batch.writes = append(batch.writes, replacement{Path: path, Files: wanted.content.Files(), Directory: true})
			}
		}
		batch.changes = append(batch.changes, change)
	}
	return batch, nil
}

func validateFrontmatter(data []byte, expectedName string) error {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return errors.New("must start with YAML frontmatter")
	}
	lines := strings.Split(text, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return errors.New("frontmatter is not closed")
	}
	var metadata struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &metadata); err != nil {
		return fmt.Errorf("invalid frontmatter: %w", err)
	}
	if metadata.Name != expectedName {
		return fmt.Errorf("frontmatter name %q does not match manifest name %q", metadata.Name, expectedName)
	}
	if strings.TrimSpace(metadata.Description) == "" {
		return errors.New("frontmatter description is required")
	}
	return nil
}
