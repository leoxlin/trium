package resource

import (
	"sort"
)

// Plugin is a complete native bundle, separate from standalone skills.
type Plugin struct {
	Name    string
	Source  string
	Files   []File
	Hash    Digest
	Enabled bool
}

func NewPlugin(name, source string, files []File) Plugin {
	files = cloneFiles(files)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return Plugin{Name: name, Source: source, Files: files, Hash: HashFiles(files), Enabled: true}
}
func (p Plugin) Key() string { return p.Name }
func (p Plugin) Fingerprint() Digest {
	if p.Enabled {
		return p.Hash
	}
	return HashFiles([]File{{Path: "disabled", Data: p.Hash[:]}})
}
