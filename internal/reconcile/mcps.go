package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/leoxlin/trium/internal/bundle"
	"github.com/leoxlin/trium/internal/manifest"
	"github.com/pelletier/go-toml/v2"
)

// planMCPs edits only declared server entries; native policy fields are preserved.
func planMCPs(ctx context.Context, servers []manifest.MCP, agent, path string) (writeBatch, error) {
	batch := writeBatch{kind: MCPs}
	if len(servers) == 0 {
		return batch, nil
	}
	var doc map[string]any
	var target observation
	var err error
	if agent == "codex" {
		var data []byte
		data, target, err = readTarget(path)
		doc = map[string]any{}
		if err == nil && target.Exists {
			err = toml.Unmarshal(data, &doc)
		}
	} else {
		doc, target, err = readJSON(path, map[string]any{})
	}
	if err != nil {
		return batch, err
	}
	batch.reads = []observation{target}
	key := "mcpServers"
	if agent == "codex" {
		key = "mcp_servers"
	}
	entries, err := objectField(doc, key)
	if err != nil {
		return batch, err
	}
	changed := false
	for _, server := range servers {
		if err := ctx.Err(); err != nil {
			return batch, err
		}
		previous, exists := entries[server.Name]
		current := map[string]any{}
		if exists {
			var ok bool
			current, ok = previous.(map[string]any)
			if !ok {
				return batch, fmt.Errorf("MCP server %s must be an object", server.Name)
			}
		}
		before, err := mcpDigest(current)
		if err != nil {
			return batch, err
		}
		next := map[string]any{}
		for key, value := range current {
			next[key] = value
		}
		// Replace the connection definition, retaining native timeouts and tool policy.
		for _, key := range []string{"command", "args", "env", "cwd", "url", "headers", "http_headers", "env_http_headers", "bearer_token_env_var", "bearerTokenEnvVar", "type", "transport"} {
			delete(next, key)
		}
		if server.Command != "" {
			next["command"] = server.Command
			args := server.Args
			if args == nil {
				args = []string{}
			}
			next["args"] = args
			if len(server.Env) > 0 {
				next["env"] = server.Env
			}
			if server.Cwd != "" {
				next["cwd"] = server.Cwd
			}
			if agent == "claude" {
				next["type"] = "stdio"
			}
		} else {
			next["url"] = server.URL
			headers := map[string]string{}
			for key, value := range server.Headers {
				headers[key] = value
			}
			if server.BearerTokenEnv != "" {
				switch agent {
				case "codex":
					next["bearer_token_env_var"] = server.BearerTokenEnv
				case "kimi":
					next["bearerTokenEnvVar"] = server.BearerTokenEnv
				case "claude":
					headers["Authorization"] = "Bearer ${" + server.BearerTokenEnv + "}"
				}
			}
			if len(headers) > 0 {
				key := "headers"
				if agent == "codex" {
					key = "http_headers"
				}
				next[key] = headers
			}
			if agent == "claude" {
				next["type"] = "http"
			}
		}
		if agent != "claude" {
			next["enabled"] = true
		}
		after, err := mcpDigest(next)
		if err != nil {
			return batch, err
		}
		change := Change{Kind: MCPs, Name: server.Name, Action: Add, After: after, Reason: "not configured"}
		if exists {
			change.Before = before
			change.Action, change.Reason = Update, "connection configuration differs"
			if before == after {
				change.Action, change.Reason = Unchanged, ""
			}
		}
		batch.changes = append(batch.changes, change)
		changed = changed || change.Action != Unchanged
		entries[server.Name] = next
	}
	if changed {
		var write replacement
		if agent == "codex" {
			data, err := toml.Marshal(doc)
			if err != nil {
				return batch, err
			}
			write = replacement{Path: path, Data: data}
		} else {
			write, err = jsonWrite(path, doc)
			if err != nil {
				return batch, err
			}
		}
		batch.writes = []replacement{write}
	}
	return batch, nil
}

func mcpDigest(value map[string]any) (bundle.Digest, error) {
	data, err := json.Marshal(value)
	return sha256.Sum256(data), err
}

func prepareMCPs(servers []manifest.MCP, baseDir string) []manifest.MCP {
	// Copy via fields so caller-owned maps and slices cannot alter prepared state.
	prepared := make([]manifest.MCP, len(servers))
	for i, server := range servers {
		server.Args = append([]string(nil), server.Args...)
		env := map[string]string{}
		for key, value := range server.Env {
			env[key] = value
		}
		server.Env = env
		headers := map[string]string{}
		for key, value := range server.Headers {
			headers[key] = value
		}
		server.Headers = headers
		if server.Cwd != "" && !filepath.IsAbs(server.Cwd) {
			server.Cwd = filepath.Join(baseDir, server.Cwd)
		}
		prepared[i] = server
	}
	sort.Slice(prepared, func(i, j int) bool { return prepared[i].Name < prepared[j].Name })
	return prepared
}
