package manifest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/leoxlin/skillets/internal/agents/shared"
	"github.com/leoxlin/skillets/internal/resource"
)

var (
	namePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	schemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	githubPattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)
	scpPattern    = regexp.MustCompile(`^(?:[^/@:\s]+@)?[^/:\s]+:[^:\s]+$`)
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
	Targets []Target
	Desired resource.Desired
}

type document struct {
	Version int         `yaml:"version"`
	Target  yaml.Node   `yaml:"target"`
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
	if len(doc.Plugins) != 0 {
		return Manifest{}, invalid("plugins", "plugins are not supported by this tracer bullet")
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
	return manifest, nil
}

func loadSkill(ctx context.Context, manifestDir string, declaration skillDecl, field string) (resource.Skill, error) {
	if declaration.Source != "" && declaration.Source != "local" {
		repository, err := repositorySource(declaration.Source)
		if err != nil {
			return resource.Skill{}, invalid(field+".source", err.Error())
		}
		return loadGitSkill(ctx, manifestDir, declaration, repository, field)
	}
	if declaration.Ref != "" {
		return resource.Skill{}, invalid(field+".ref", "is only valid with a repository source")
	}
	if declaration.Path == "" {
		return resource.Skill{}, invalid(field+".path", "is required when source is local or omitted")
	}
	root, err := sourcePath(manifestDir, declaration.Path)
	if err != nil {
		return resource.Skill{}, invalid(field+".path", err.Error())
	}
	files, err := resource.Snapshot(root)
	if err != nil {
		return resource.Skill{}, invalid(field+".path", err.Error())
	}
	return skillFromFiles(declaration.Name, root, files, field+".path")
}

func repositorySource(source string) (string, error) {
	if hasControl(source) || strings.TrimSpace(source) == "" || strings.HasPrefix(source, "-") {
		return "", errors.New("must be a Git URL or GitHub shorthand without control characters")
	}
	if strings.HasPrefix(source, "github:") || strings.HasPrefix(source, "github.com/") {
		repository := strings.TrimPrefix(strings.TrimPrefix(source, "github:"), "github.com/")
		if !githubPattern.MatchString(repository) {
			return "", errors.New("GitHub shorthand must be owner/repo")
		}
		return "https://github.com/" + repository, nil
	}
	if githubPattern.MatchString(source) {
		return "https://github.com/" + source, nil
	}
	if strings.HasPrefix(source, "git:") && !strings.HasPrefix(source, "git://") {
		source = strings.TrimPrefix(source, "git:")
	}
	source = strings.TrimPrefix(source, "git+")
	if strings.Contains(source, "://") {
		parsed, err := url.Parse(source)
		if err != nil || parsed.Host == "" {
			return "", errors.New("invalid Git repository URL")
		}
		switch parsed.Scheme {
		case "https", "http", "ssh", "git":
			return source, nil
		default:
			return "", errors.New("unsupported Git repository URL scheme")
		}
	}
	if scpPattern.MatchString(source) && !strings.HasPrefix(source, "./") && !strings.HasPrefix(source, "../") {
		return source, nil
	}
	return "", errors.New("must be a Git URL or GitHub shorthand; use path for a local skill")
}

func loadGitSkill(ctx context.Context, manifestDir string, declaration skillDecl, repository, field string) (resource.Skill, error) {
	ref := declaration.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if hasControl(ref) || strings.ContainsFunc(ref, unicode.IsSpace) || strings.HasPrefix(ref, "-") ||
		strings.ContainsAny(ref, ":*?[^~\\") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return resource.Skill{}, invalid(field+".ref", "must be a branch, tag, or full commit hash")
	}
	bundlePath := path.Clean(declaration.Path)
	if hasControl(declaration.Path) || strings.ContainsAny(declaration.Path, `\:`) || path.IsAbs(bundlePath) ||
		bundlePath == ".." || strings.HasPrefix(bundlePath, "../") {
		return resource.Skill{}, invalid(field+".path", "must stay inside the repository and contain no control characters")
	}

	source, files, err := shared.SnapshotGit(ctx, manifestDir, repository, ref, bundlePath)
	if err != nil {
		var unsafe *resource.UnsafePathError
		if errors.As(err, &unsafe) {
			return resource.Skill{}, invalid(field+".path", unsafe.Reason)
		}
		return resource.Skill{}, fmt.Errorf("%s.source: %w", field, err)
	}
	return skillFromFiles(declaration.Name, source, files, field+".source")
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
