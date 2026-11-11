# trium

Manage skills, plugins, and MCP servers for Codex, Claude Code, and Kimi Code from one YAML manifest.
Keep your agents configured consistently, preview changes before applying them, and leave undeclared resources alone.
trium backs up files before replacing them.

## Quickstart

Build the CLI with Go 1.26 or later:

```bash
go build -o trium ./cmd/trium
```

Create `~/.config/trium.yaml`:

```yaml
version: 1
target:
  - agent: codex
  - agent: claude
  - agent: kimi
skills:
  - name: code-review
    source: local
    path: skills/code-review
```

Point `path` at an existing skill directory, relative to the manifest. It must contain a `SKILL.md` with
`name: code-review` and a non-empty `description` in its YAML frontmatter. Keep only the agents you use in `target`.

Preview, then apply:

```bash
./trium
./trium apply
```

Pass a manifest path to either command to use a different file, such as `./trium apply ./trium.yaml`.
See [the example manifest](trium.example.yaml) for Git sources, plugins, and MCP servers, or
[the schema](trium.schema.yaml) for all configuration options.

[MIT License](LICENSE)
