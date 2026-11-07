package manifest

import (
	"context"
	"errors"
	"fmt"

	"github.com/leoxlin/skillets/internal/resource"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	schemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	githubPattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)
	scpPattern    = regexp.MustCompile(`^(?:[^/@:\s]+@)?[^/:\s]+:[^:\s]+$`)
)

func loadBundle(ctx context.Context, manifestDir string, declaration bundleDecl, field string) (string, []resource.File, error) {
	if declaration.Source != "" && declaration.Source != "local" {
		repository, err := repositorySource(declaration.Source)
		if err != nil {
			return "", nil, invalid(field+".source", err.Error())
		}
		return loadGitBundle(ctx, manifestDir, declaration, repository, field)
	}
	if declaration.Ref != "" {
		return "", nil, invalid(field+".ref", "is only valid with a repository source")
	}
	if declaration.Path == "" {
		return "", nil, invalid(field+".path", "is required when source is local or omitted")
	}
	root, err := sourcePath(manifestDir, declaration.Path)
	if err != nil {
		return "", nil, invalid(field+".path", err.Error())
	}
	files, err := resource.Snapshot(root)
	if err != nil {
		return "", nil, invalid(field+".path", err.Error())
	}
	return root, files, nil
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

func loadGitBundle(ctx context.Context, manifestDir string, declaration bundleDecl, repository, field string) (string, []resource.File, error) {
	ref := declaration.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if hasControl(ref) || strings.ContainsFunc(ref, unicode.IsSpace) || strings.HasPrefix(ref, "-") ||
		strings.ContainsAny(ref, ":*?[^~\\") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return "", nil, invalid(field+".ref", "must be a branch, tag, or full commit hash")
	}
	bundlePath := path.Clean(declaration.Path)
	if hasControl(declaration.Path) || strings.ContainsAny(declaration.Path, `\:`) || path.IsAbs(bundlePath) ||
		bundlePath == ".." || strings.HasPrefix(bundlePath, "../") {
		return "", nil, invalid(field+".path", "must stay inside the repository and contain no control characters")
	}

	source, files, err := resource.SnapshotGit(ctx, manifestDir, repository, ref, bundlePath)
	if err != nil {
		var unsafe *resource.UnsafePathError
		if errors.As(err, &unsafe) {
			return "", nil, invalid(field+".path", unsafe.Reason)
		}
		return "", nil, fmt.Errorf("%s.source: %w", field, err)
	}
	return source, files, nil
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
