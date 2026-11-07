package manifest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/leoxlin/skillets/internal/resource"
)

var (
	namePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	schemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

type Target struct {
	Agent string
	Path  string
}

type Manifest struct {
	Path    string
	Target  Target
	Desired resource.Desired
}

type document struct {
	Version int         `yaml:"version"`
	Target  targetDecl  `yaml:"target"`
	Skills  []skillDecl `yaml:"skills"`
	Plugins []yaml.Node `yaml:"plugins"`
	MCPs    []yaml.Node `yaml:"mcps"`
}

type targetDecl struct {
	Agent string `yaml:"agent"`
	Path  string `yaml:"path"`
}

type skillDecl struct {
	Name   string `yaml:"name"`
	Source string `yaml:"source"`
	GitHub string `yaml:"github"`
	Path   string `yaml:"path"`
	Ref    string `yaml:"ref"`
}

func Load(ctx context.Context, path string) (Manifest, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Manifest{}, err
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", absolute, err)
	}
	if err := rejectNulls(data); err != nil {
		return Manifest{}, err
	}
	var doc document
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return Manifest{}, &ValidationError{Field: "manifest", Message: err.Error()}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Manifest{}, &ValidationError{Field: "manifest", Message: "multiple YAML documents are not allowed"}
	}
	if doc.Version != 1 {
		return Manifest{}, invalid("version", "must be 1")
	}
	if doc.Target.Agent != "codex" && doc.Target.Agent != "kimi" {
		return Manifest{}, invalid("target.agent", "must be codex or kimi")
	}
	if err := validateTargetPath(doc.Target.Path); err != nil {
		return Manifest{}, err
	}
	if len(doc.Plugins) != 0 {
		return Manifest{}, invalid("plugins", "plugins are not supported by this tracer bullet")
	}
	if len(doc.MCPs) != 0 {
		return Manifest{}, invalid("mcps", "MCPs are not supported by this tracer bullet")
	}

	manifest := Manifest{
		Path:   absolute,
		Target: Target{Agent: doc.Target.Agent, Path: doc.Target.Path},
	}
	seen := make(map[string]struct{}, len(doc.Skills))
	for i, declaration := range doc.Skills {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		field := fmt.Sprintf("skills[%d]", i)
		if !namePattern.MatchString(declaration.Name) {
			return Manifest{}, invalid(field+".name", "must match "+namePattern.String())
		}
		if _, exists := seen[declaration.Name]; exists {
			return Manifest{}, invalid(field+".name", "duplicate skill name")
		}
		seen[declaration.Name] = struct{}{}
		skill, err := loadSkill(filepath.Dir(absolute), declaration, field)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Desired.Skills = append(manifest.Desired.Skills, skill)
	}
	return manifest, nil
}

func loadSkill(manifestDir string, declaration skillDecl, field string) (resource.Skill, error) {
	if declaration.GitHub != "" {
		return resource.Skill{}, invalid(field+".github", "GitHub sources are not supported by this tracer bullet")
	}
	if declaration.Source == "" {
		return resource.Skill{}, invalid(field+".source", "exactly one local source is required")
	}
	if declaration.Path != "" || declaration.Ref != "" {
		return resource.Skill{}, invalid(field, "path and ref are only valid with a GitHub source")
	}
	root, err := sourcePath(manifestDir, declaration.Source)
	if err != nil {
		return resource.Skill{}, invalid(field+".source", err.Error())
	}
	files, err := resource.Snapshot(root)
	if err != nil {
		return resource.Skill{}, invalid(field+".source", err.Error())
	}
	skillFile, ok := resource.FindFile(files, "SKILL.md")
	if !ok {
		return resource.Skill{}, invalid(field+".source", "bundle must contain SKILL.md")
	}
	if err := validateFrontmatter(skillFile.Data, declaration.Name); err != nil {
		return resource.Skill{}, invalid(field+".source/SKILL.md", err.Error())
	}
	return resource.NewSkill(declaration.Name, root, files), nil
}

func sourcePath(manifestDir, source string) (string, error) {
	if hasControl(source) || filepath.IsAbs(source) || schemePattern.MatchString(source) {
		return "", errors.New("must be a relative path without control characters")
	}
	clean := filepath.Clean(source)
	root := filepath.Join(manifestDir, clean)
	relative, err := filepath.Rel(manifestDir, root)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("must stay inside the manifest directory")
	}
	current := manifestDir
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symlink", current)
		}
	}
	return root, nil
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

func validateTargetPath(path string) error {
	if path == "" {
		return nil
	}
	if hasControl(path) || (!filepath.IsAbs(path) && !strings.HasPrefix(path, "~/")) {
		return invalid("target.path", "must be absolute or start with ~/")
	}
	return nil
}

func rejectNulls(data []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return &ValidationError{Field: "manifest", Message: err.Error()}
	}
	var walk func(*yaml.Node) error
	walk = func(node *yaml.Node) error {
		if node.Tag == "!!null" {
			return invalid("manifest", "null values are not allowed")
		}
		for _, child := range node.Content {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(&root)
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

func invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}
