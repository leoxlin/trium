package manifest_test

import (
	"errors"
	"github.com/leoxlin/trium/internal/manifest"
	"testing"
)

func TestParseOnlyValidatesDeclarations(t *testing.T) {
	spec, err := manifest.Parse([]byte("version: 1\ntarget: [{agent: codex}, {agent: kimi, path: ~/other}]\nskills:\n  - name: review\n    path: missing-directory\nplugins:\n  - name: review\n    source: example/nonexistent-repository\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Targets) != 2 || spec.Targets[1].Path != "~/other" || len(spec.Skills) != 1 || len(spec.Plugins) != 1 {
		t.Fatalf("declarations = %#v", spec)
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	tests := map[string]string{
		"empty targets":         "version: 1\ntarget: []\n",
		"missing target":        "version: 1\n",
		"invalid list agent":    "version: 1\ntarget:\n  - agent: codex\n  - agent: nope\n",
		"unknown target field":  "version: 1\ntarget:\n  - agent: kimi\n    typo: true\n",
		"duplicate targets":     "version: 1\ntarget:\n  - agent: kimi\n  - agent: kimi\n",
		"scalar target":         "version: 1\ntarget: kimi\n",
		"null list target":      "version: 1\ntarget: [null]\n",
		"relative list path":    "version: 1\ntarget:\n  - agent: kimi\n    path: relative\n",
		"duplicate target keys": "version: 1\ntarget:\n  - agent: kimi\n    agent: codex\n",

		"plugins":         "version: 1\ntarget:\n  agent: kimi\nplugins:\n  - name: nope\n    source: nope\n",
		"legacy github":   "version: 1\ntarget:\n  agent: codex\nskills:\n  - name: review\n    github: owner/repo\n",
		"unknown field":   "version: 1\ntarget:\n  agent: kimi\nunknown: true\n",
		"null":            "version: 1\ntarget:\n  agent: kimi\nskills: null\n",
		"relative target": "version: 1\ntarget:\n  agent: kimi\n  path: relative\n",
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := manifest.Parse([]byte(contents))
			var validation *manifest.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParseGitRejectsInvalidDeclarations(t *testing.T) {
	for name, fields := range map[string]string{
		"duplicate source": "source: example/skills\n    source: local", "missing": "ref: main",
		"escape": "source: example/skills\n    path: ../outside", "absolute": "source: example/skills\n    path: /outside",
		"backslash": `source: example/skills` + "\n    path: 'a\\b'", "ref option": "source: example/skills\n    ref: --all",
		"ref whitespace": "source: example/skills\n    ref: 'bad ref'", "url option": "source: git:--upload-pack=bad",
		"refspec": "source: example/skills\n    ref: 'main:refs/heads/other'", "wildcard": "source: example/skills\n    ref: '*'",
		"legacy git": "git: repo", "empty git": "source: 'git:'", "empty source": "source: ''",
		"bad shorthand": "source: github:owner", "escaping shorthand": "source: github:../repo",
		"bad url": "source: https://", "unsupported scheme": "source: ext::command",
		"local ref": "path: skill\n    ref: main", "local source": "source: ./skill\n    path: nested",
		"absolute source": "source: /tmp/skill", "git local source": "source: git:./skill",
		"file source":        "source: file:///tmp/skill",
		"local missing path": "source: local", "explicit local ref": "source: local\n    path: skill\n    ref: main",
	} {
		t.Run(name, func(t *testing.T) {
			contents := "version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    " + fields + "\n"
			_, err := manifest.Parse([]byte(contents))
			var validation *manifest.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestMCPDeclarations(t *testing.T) {
	spec, err := manifest.Parse([]byte(`version: 1
target: {agent: codex}
mcps:
 - name: local
   command: server
   args: [one, two]
   env: {TOKEN: literal}
   cwd: .
 - name: remote
   url: https://example.com/mcp
   headers: {X-Client: trium}
   bearer_token_env: MCP_TOKEN
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.MCPs) != 2 || spec.MCPs[0].Cwd != "." || spec.MCPs[1].BearerTokenEnv != "MCP_TOKEN" {
		t.Fatalf("MCPs = %#v", spec.MCPs)
	}
	for name, declaration := range map[string]string{
		"missing transport":      "name: server",
		"both transports":        "name: server, command: server, url: https://example.com",
		"unknown field":          "name: server, command: server, typo: true",
		"duplicate keys":         "name: server, command: one, command: two",
		"invalid name":           "name: BAD, command: server",
		"empty command":          "name: server, command: ''",
		"empty cwd":              "name: server, command: server, cwd: ''",
		"empty url with command": "name: server, command: server, url: ''",
		"empty token":            "name: server, url: https://example.com, bearer_token_env: ''",
		"stdio headers":          "name: server, command: server, headers: {}",
		"stdio token":            "name: server, command: server, bearer_token_env: TOKEN",
		"http args":              "name: server, url: https://example.com, args: []",
		"http env":               "name: server, url: https://example.com, env: {}",
		"http cwd":               "name: server, url: https://example.com, cwd: .",
		"invalid url":            "name: server, url: file:///tmp/server",
		"missing host":           "name: server, url: https:///mcp",
		"invalid token":          "name: server, url: https://example.com, bearer_token_env: bad-name",
		"duplicate auth":         "name: server, url: https://example.com, bearer_token_env: TOKEN, headers: {authorization: secret}",
		"invalid header":         "name: server, url: https://example.com, headers: {'bad header': value}",
		"header injection":       `name: server, url: https://example.com, headers: {X-Test: "value\r\nInjected: value"}`,
		"invalid env":            "name: server, command: server, env: {'bad=name': value}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := manifest.Parse([]byte("version: 1\ntarget: {agent: codex}\nmcps: [{" + declaration + "}]\n"))
			var validation *manifest.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	_, err = manifest.Parse([]byte("version: 1\ntarget: {agent: codex}\nmcps: [{name: same, command: one}, {name: same, command: two}]\n"))
	if err == nil {
		t.Fatal("duplicate MCP names accepted")
	}
}
