package plugin

import (
	"encoding/json"
	"fmt"

	"github.com/leoxlin/skillets/internal/resource"
)

func Validate(name, agent string, files []resource.File) error {
	paths := map[string][]string{
		"codex":  {".codex-plugin/plugin.json"},
		"claude": {".claude-plugin/plugin.json"},
		"kimi":   {"kimi.plugin.json", ".kimi-plugin/plugin.json"},
	}
	for _, path := range paths[agent] {
		file, ok := resource.FindFile(files, path)
		if !ok {
			continue
		}
		var metadata struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(file.Data, &metadata); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		identity := metadata.Name
		if agent == "kimi" && metadata.ID != "" {
			identity = metadata.ID
		}
		if identity != name {
			return fmt.Errorf("%s: plugin name or ID %q does not match manifest name %q", path, identity, name)
		}
		return nil
	}
	return fmt.Errorf("%s plugin must contain %v", agent, paths[agent])
}

func Version(plugin resource.Plugin, agent string) (string, error) {
	file, _ := resource.FindFile(plugin.Files, "."+agent+"-plugin/plugin.json")
	var metadata struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(file.Data, &metadata); err != nil {
		return "", err
	}
	if metadata.Version == "" {
		return "0.1.0", nil
	}
	return metadata.Version, nil
}
