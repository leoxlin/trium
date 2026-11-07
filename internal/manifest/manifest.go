package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/leoxlin/skillets/internal/bundle"
	"gopkg.in/yaml.v3"
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
	Targets []Target
	Skills  []Declaration
	Plugins []Declaration
	MCPs    []MCP
}

// MCP declares either a stdio command or a Streamable HTTP endpoint.
type MCP struct {
	Name           string            `yaml:"name"`
	Command        string            `yaml:"command,omitempty"`
	Args           []string          `yaml:"args,omitempty"`
	Env            map[string]string `yaml:"env,omitempty"`
	Cwd            string            `yaml:"cwd,omitempty"`
	URL            string            `yaml:"url,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty"`
	BearerTokenEnv string            `yaml:"bearer_token_env,omitempty"`
}

// ValidateMCPs also validates declarations constructed without Parse.
func ValidateMCPs(servers []MCP) error {
	seen := map[string]bool{}
	for i, server := range servers {
		field := fmt.Sprintf("mcps[%d]", i)
		if !namePattern.MatchString(server.Name) || seen[server.Name] {
			return invalid(field+".name", "invalid or duplicate resource name")
		}
		seen[server.Name] = true
		if (server.Command == "") == (server.URL == "") {
			return invalid(field, "requires exactly one of command or url")
		}
		if server.Command != "" {
			if strings.TrimSpace(server.Command) == "" || hasControl(server.Command) {
				return invalid(field+".command", "must be a non-empty command without control characters")
			}
			if server.Headers != nil || server.BearerTokenEnv != "" {
				return invalid(field, "stdio servers cannot have headers or bearer_token_env")
			}
		} else {
			endpoint, err := url.Parse(server.URL)
			if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || hasControl(server.URL) {
				return invalid(field+".url", "must be an absolute HTTP or HTTPS URL")
			}
			if server.Args != nil || server.Env != nil || server.Cwd != "" {
				return invalid(field, "HTTP servers cannot have args, env, or cwd")
			}
		}
		if hasControl(server.Cwd) {
			return invalid(field+".cwd", "must not contain control characters")
		}
		for key, value := range server.Env {
			if !envPattern.MatchString(key) || strings.ContainsRune(value, 0) {
				return invalid(field+".env", "requires environment variable names and values without NUL")
			}
		}
		for _, arg := range server.Args {
			if strings.ContainsRune(arg, 0) {
				return invalid(field+".args", "must not contain NUL")
			}
		}
		for key, value := range server.Headers {
			if !headerPattern.MatchString(key) || hasControl(value) {
				return invalid(field+".headers", "requires HTTP header names and values without control characters")
			}
			if server.BearerTokenEnv != "" && strings.EqualFold(key, "Authorization") {
				return invalid(field+".headers", "Authorization and bearer_token_env are mutually exclusive")
			}
		}
		if server.BearerTokenEnv != "" && !envPattern.MatchString(server.BearerTokenEnv) {
			return invalid(field+".bearer_token_env", "must be an environment variable name")
		}
	}
	return nil
}

var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var headerPattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

type Declaration struct {
	Name   string
	Source bundle.Reference
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

// Parse validates declarations without reading their sources.
func Parse(data []byte) (Manifest, error) {
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

	result := Manifest{Targets: selected}
	for i, node := range doc.MCPs {
		data, err := yaml.Marshal(&node)
		if err != nil {
			return Manifest{}, err
		}
		var server MCP
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&server); err != nil {
			return Manifest{}, invalid(fmt.Sprintf("mcps[%d]", i), err.Error())
		}
		// Check presence as well as values, so empty fields cannot mix transports.
		for j := 0; j < len(node.Content); j += 2 {
			key := node.Content[j].Value
			value := node.Content[j+1]
			if (key == "command" || key == "url" || key == "cwd" || key == "bearer_token_env") && value.Value == "" {
				return Manifest{}, invalid(fmt.Sprintf("mcps[%d].%s", i, key), "must not be empty")
			}
		}
		result.MCPs = append(result.MCPs, server)
	}
	if err := ValidateMCPs(result.MCPs); err != nil {
		return Manifest{}, err
	}
	for _, group := range []struct {
		kind         string
		declarations []bundleDecl
		output       *[]Declaration
	}{
		{"skills", doc.Skills, &result.Skills}, {"plugins", doc.Plugins, &result.Plugins},
	} {
		seen := map[string]bool{}
		for i, declaration := range group.declarations {
			field := fmt.Sprintf("%s[%d]", group.kind, i)
			if !namePattern.MatchString(declaration.Name) {
				return Manifest{}, invalid(field+".name", "must match "+namePattern.String())
			}
			if seen[declaration.Name] {
				return Manifest{}, invalid(field+".name", "duplicate resource name")
			}
			seen[declaration.Name] = true
			source, err := bundle.ParseReference(declaration.Source, declaration.Path, declaration.Ref)
			if err != nil {
				var reference *bundle.ReferenceError
				if errors.As(err, &reference) {
					return Manifest{}, invalid(field+"."+reference.Field, reference.Message)
				}
				return Manifest{}, err
			}
			*group.output = append(*group.output, Declaration{Name: declaration.Name, Source: source})
		}
	}
	return result, nil
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
