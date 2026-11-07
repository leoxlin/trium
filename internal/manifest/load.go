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

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

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
	Targets []Target
	Desired resource.Desired
}

type document struct {
	Version int          `yaml:"version"`
	Target  yaml.Node    `yaml:"target"`
	Skills  []bundleDecl `yaml:"skills"`
	Plugins []bundleDecl `yaml:"plugins"`
	MCPs    []yaml.Node  `yaml:"mcps"`
}

type targetDecl struct {
	Agent string `yaml:"agent"`
	Path  string `yaml:"path"`
}

type bundleDecl struct {
	Name   string `yaml:"name"`
	Source string `yaml:"source"`
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
	targets := []*yaml.Node{&doc.Target}
	if doc.Target.Kind == yaml.SequenceNode {
		targets = doc.Target.Content
	}
	if len(targets) == 0 {
		return Manifest{}, invalid("target", "must contain at least one target")
	}
	var selected []Target
	seenTargets := make(map[Target]bool)
	for i, node := range targets {
		field := "target"
		if doc.Target.Kind == yaml.SequenceNode {
			field = fmt.Sprintf("target[%d]", i)
		}
		if node.Kind != yaml.MappingNode {
			return Manifest{}, invalid(field, "must be an agent mapping")
		}
		data, err := yaml.Marshal(node)
		if err != nil {
			return Manifest{}, err
		}
		var declaration targetDecl
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&declaration); err != nil {
			return Manifest{}, invalid(field, err.Error())
		}
		if declaration.Agent != "codex" && declaration.Agent != "claude" && declaration.Agent != "kimi" {
			return Manifest{}, invalid(field+".agent", "must be codex, claude, or kimi")
		}
		if err := validateTargetPath(declaration.Path); err != nil {
			return Manifest{}, invalid(field+".path", err.Error())
		}
		target := Target{Agent: declaration.Agent, Path: declaration.Path}
		if seenTargets[target] {
			return Manifest{}, invalid(field, "duplicate target")
		}
		seenTargets[target] = true
		selected = append(selected, target)
	}
	if len(doc.MCPs) != 0 {
		return Manifest{}, invalid("mcps", "MCPs are not supported by this tracer bullet")
	}

	manifest := Manifest{
		Path:    absolute,
		Targets: selected,
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
		skill, err := loadSkill(ctx, filepath.Dir(absolute), declaration, field)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Desired.Skills = append(manifest.Desired.Skills, skill)
	}
	seenPlugins := make(map[string]bool)
	for i, declaration := range doc.Plugins {
		field := fmt.Sprintf("plugins[%d]", i)
		if !namePattern.MatchString(declaration.Name) {
			return Manifest{}, invalid(field+".name", "must match "+namePattern.String())
		}
		if seenPlugins[declaration.Name] {
			return Manifest{}, invalid(field+".name", "duplicate plugin name")
		}
		seenPlugins[declaration.Name] = true
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		plugin, err := loadPlugin(ctx, filepath.Dir(absolute), declaration, selected, field)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Desired.Plugins = append(manifest.Desired.Plugins, plugin)
	}
	return manifest, nil
}

func validateTargetPath(path string) error {
	if path == "" {
		return nil
	}
	if hasControl(path) || (!filepath.IsAbs(path) && !strings.HasPrefix(path, "~/")) {
		return errors.New("must be absolute or start with ~/")
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
