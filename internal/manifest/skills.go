package manifest

import (
	"context"
	"errors"
	"fmt"

	"github.com/leoxlin/skillets/internal/resource"
	"gopkg.in/yaml.v3"
	"strings"
)

func loadSkill(ctx context.Context, manifestDir string, declaration bundleDecl, field string) (resource.Skill, error) {
	source, files, err := loadBundle(ctx, manifestDir, declaration, field)
	if err != nil {
		return resource.Skill{}, err
	}
	metadataField := field + ".path"
	if declaration.Source != "" && declaration.Source != "local" {
		metadataField = field + ".source"
	}
	return skillFromFiles(declaration.Name, source, files, metadataField)
}

func skillFromFiles(name, source string, files []resource.File, field string) (resource.Skill, error) {
	skillFile, ok := resource.FindFile(files, "SKILL.md")
	if !ok {
		return resource.Skill{}, invalid(field, "bundle must contain SKILL.md")
	}
	if err := validateFrontmatter(skillFile.Data, name); err != nil {
		return resource.Skill{}, invalid(field+"/SKILL.md", err.Error())
	}
	return resource.NewSkill(name, source, files), nil
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
