package resource

import "sort"

type Skill struct {
	Name   string
	Source string
	Files  []File
	Hash   Digest
}

func NewSkill(name, source string, files []File) Skill {
	files = cloneFiles(files)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return Skill{Name: name, Source: source, Files: files, Hash: HashFiles(files)}
}

func (s Skill) Key() string {
	return s.Name
}

func (s Skill) Fingerprint() Digest {
	return s.Hash
}
