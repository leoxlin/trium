package bundle

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Reference can only be constructed by ParseReference. Its zero value is invalid.
type Reference struct {
	repository, path, ref string
	valid                 bool
}

type ReferenceError struct{ Field, Message string }

func (e *ReferenceError) Error() string { return e.Field + ": " + e.Message }

var (
	schemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	githubPattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)
	scpPattern    = regexp.MustCompile(`^(?:[^/@:\s]+@)?[^/:\s]+:[^:\s]+$`)
)

func ParseReference(source, location, ref string) (Reference, error) {
	invalid := func(field, message string) (Reference, error) {
		return Reference{}, &ReferenceError{Field: field, Message: message}
	}
	if source == "" || source == "local" {
		if ref != "" {
			return invalid("ref", "is only valid with a repository source")
		}
		if location == "" {
			return invalid("path", "is required when source is local or omitted")
		}
		if hasControl(location) || !filepath.IsLocal(location) || schemePattern.MatchString(location) {
			return invalid("path", "must stay inside the manifest directory without control characters")
		}
		return Reference{path: filepath.Clean(location), valid: true}, nil
	}
	repository, err := repositorySource(source)
	if err != nil {
		return invalid("source", err.Error())
	}
	if ref == "" {
		ref = "HEAD"
	}
	if hasControl(ref) || strings.ContainsFunc(ref, unicode.IsSpace) || strings.HasPrefix(ref, "-") ||
		strings.ContainsAny(ref, ":*?[^~\\") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return invalid("ref", "must be a branch, tag, or full commit hash")
	}
	cleaned := path.Clean(location)
	if hasControl(location) || strings.ContainsAny(location, `\:`) || path.IsAbs(cleaned) ||
		cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return invalid("path", "must stay inside the repository and contain no control characters")
	}
	return Reference{repository: repository, path: cleaned, ref: ref, valid: true}, nil
}

// Acquire owns temporary acquisition resources and returns independent bytes.
func Acquire(ctx context.Context, baseDir string, source Reference) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if !source.valid {
		return Snapshot{}, &ReferenceError{Field: "source", Message: "invalid reference"}
	}
	if !filepath.IsAbs(baseDir) {
		return Snapshot{}, fmt.Errorf("source base directory must be absolute")
	}
	if source.repository == "" {
		return ReadDir(ctx, filepath.Join(baseDir, source.path))
	}
	origin, files, err := snapshotGit(ctx, baseDir, source.repository, source.ref, source.path)
	if err != nil {
		return Snapshot{}, err
	}
	return newSnapshot(origin, files), nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
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
	return "", errors.New("must be a Git URL or GitHub shorthand; use path for a local bundle")
}
