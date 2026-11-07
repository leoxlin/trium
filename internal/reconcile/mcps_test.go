package reconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leoxlin/skillets/internal/manifest"
	"github.com/leoxlin/skillets/internal/reconcile"
	"github.com/pelletier/go-toml/v2"
)

func mcpPath(f *fixture) string {
	switch f.kind {
	case "codex":
		return filepath.Join(f.home, "config.toml")
	case "claude":
		return filepath.Join(f.home, ".claude.json")
	default:
		return filepath.Join(f.home, "mcp.json")
	}
}

func mcpDocument(t *testing.T, f *fixture) map[string]any {
	t.Helper()
	var doc map[string]any
	var err error
	if f.kind == "codex" {
		err = toml.Unmarshal(read(t, mcpPath(f)), &doc)
	} else {
		err = json.Unmarshal(read(t, mcpPath(f)), &doc)
	}
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestMCPLifecycle(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "kimi"} {
		t.Run(agent, func(t *testing.T) {
			f := newFixture(t, agent)
			// Use real skill and plugin declarations to cover shared Codex config writes.
			f.prepare("standalone", "plugin")
			spec, err := manifest.Parse([]byte("version: 1\ntarget: {agent: " + agent + "}\nskills: [{name: review, path: skill}]\nplugins: [{name: review, path: plugin}]\n"))
			if err != nil {
				t.Fatal(err)
			}
			spec.MCPs = []manifest.MCP{
				{Name: "remote", URL: "https://example.com/mcp", Headers: map[string]string{"X-Client": "skillet"}, BearerTokenEnv: "MCP_TEST_TOKEN"},
				{Name: "review", Command: "never-execute-this-mcp-server", Args: []string{"--test"}, Env: map[string]string{"SETTING": "literal"}},
			}
			if agent != "claude" {
				spec.MCPs[1].Cwd = "work"
			}
			prepared, err := reconcile.Prepare(context.Background(), spec, f.source, f.homes)
			if err != nil {
				t.Fatal(err)
			}
			initial := plan(t, prepared)
			action(t, initial, reconcile.MCPs, reconcile.Add)
			if _, err := os.Stat(f.home); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("preview wrote target: %v", err)
			}
			// Prepared MCP input is independent of caller mutations.
			spec.MCPs[0].Headers["X-Client"] = "changed"
			spec.MCPs[1].Args[0] = "changed"
			result := apply(t, prepared)
			if len(result.Applied) != 4 || len(result.Backups) != 3 {
				t.Fatalf("report = %#v", result)
			}
			action(t, plan(t, prepared), reconcile.Plugins, reconcile.Unchanged)
			action(t, plan(t, prepared), reconcile.MCPs, reconcile.Unchanged)
			if result := apply(t, prepared); len(result.Applied) != 0 || len(result.Backups) != 0 {
				t.Fatalf("second apply = %#v", result)
			}
			doc := mcpDocument(t, f)
			key := "mcpServers"
			if agent == "codex" {
				key = "mcp_servers"
			}
			entries := doc[key].(map[string]any)
			remote := entries["remote"].(map[string]any)
			local := entries["review"].(map[string]any)
			args := local["args"].([]any)
			if args[0] != "--test" {
				t.Fatalf("args = %#v", args)
			}
			switch agent {
			case "codex":
				if remote["bearer_token_env_var"] != "MCP_TEST_TOKEN" || remote["http_headers"].(map[string]any)["X-Client"] != "skillet" {
					t.Fatalf("remote = %#v", remote)
				}
			case "kimi":
				if remote["bearerTokenEnvVar"] != "MCP_TEST_TOKEN" || remote["headers"].(map[string]any)["X-Client"] != "skillet" {
					t.Fatalf("remote = %#v", remote)
				}
			case "claude":
				if remote["type"] != "http" || remote["headers"].(map[string]any)["Authorization"] != "Bearer ${MCP_TEST_TOKEN}" {
					t.Fatalf("remote = %#v", remote)
				}
			}
			if agent != "claude" && local["cwd"] != filepath.Join(f.source, "work") {
				t.Fatalf("cwd = %v", local["cwd"])
			}
			// Preserve undeclared servers, unrelated settings, and per-server policy.
			entries["unrelated"] = map[string]any{"command": "keep"}
			doc["unrelated"] = true
			local["custom_policy"] = "keep"
			if agent != "claude" {
				local["enabled"] = false
			}
			var data []byte
			if agent == "codex" {
				data, err = toml.Marshal(doc)
			} else {
				data, err = json.Marshal(doc)
			}
			if err != nil {
				t.Fatal(err)
			}
			write(t, mcpPath(f), data)
			// Change transport; stale connection fields must disappear.
			spec.MCPs = []manifest.MCP{{Name: "review", URL: "http://localhost:8888/mcp"}}
			prepared, err = reconcile.Prepare(context.Background(), spec, f.source, f.homes)
			if err != nil {
				t.Fatal(err)
			}
			action(t, plan(t, prepared), reconcile.MCPs, reconcile.Update)
			apply(t, prepared)
			doc = mcpDocument(t, f)
			entries = doc[key].(map[string]any)
			local = entries["review"].(map[string]any)
			if doc["unrelated"] != true || entries["unrelated"].(map[string]any)["command"] != "keep" || local["custom_policy"] != "keep" {
				t.Fatalf("preservation failed: %#v", doc)
			}
			for _, field := range []string{"command", "args", "env", "cwd"} {
				if _, ok := local[field]; ok {
					t.Fatalf("stale field %s", field)
				}
			}
			if entries["remote"] == nil {
				t.Fatal("undeclared MCP removed")
			}
			action(t, plan(t, prepared), reconcile.MCPs, reconcile.Unchanged)
		})
	}
}

func TestMCPPreflight(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "kimi"} {
		t.Run(agent, func(t *testing.T) {
			for _, contents := range []string{"invalid", `{"mcpServers":[]}`, `{"mcpServers":{"server":false}}`, `{"mcpServers":{},"mcpServers":{}}`} {
				f := newFixture(t, agent)
				spec := manifest.Manifest{Targets: []manifest.Target{{Agent: agent}}, MCPs: []manifest.MCP{{Name: "server", Command: "server"}}}
				prepared, err := reconcile.Prepare(context.Background(), spec, f.source, f.homes)
				if err != nil {
					t.Fatal(err)
				}
				if agent == "codex" {
					if strings.HasPrefix(contents, "{") {
						contents = "mcp_servers = []"
					}
				}
				write(t, mcpPath(f), []byte(contents))
				if _, err := prepared.Apply(context.Background()); err == nil {
					t.Fatalf("accepted %s", contents)
				}
				if string(read(t, mcpPath(f))) != contents {
					t.Fatal("invalid document changed")
				}
			}
		})
	}
	f := newFixture(t, "codex")
	spec := manifest.Manifest{Targets: []manifest.Target{{Agent: "codex"}, {Agent: "claude"}}, MCPs: []manifest.MCP{{Name: "server", Command: "server", Cwd: "."}}}
	if _, err := reconcile.Prepare(context.Background(), spec, f.source, f.homes); err == nil {
		t.Fatal("Claude cwd accepted")
	}
	if _, err := os.Stat(f.home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preflight created target")
	}
}
