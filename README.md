# skillets

<div align="center">
  <img src="skillets.png" alt="skillets" width="250">
  <br>
  An easy way to manage my agent skills — and more.
</div>

## Tracer bullet

The current implementation reconciles local and Git-sourced directory-form skills for Codex, Claude Code, and Kimi Code.
Plugins and MCP servers remain in the design but are not implemented.

Build the CLI:

```bash
go build ./cmd/skillet
```

Create `~/.config/skillet.yaml`:

```yaml
version: 1
target:
  agent: codex # codex, claude, or kimi
skills:
  - name: code-review
    source: local
    path: skills/code-review
```

To manage multiple agents with the same skills, use a non-empty target list:

```yaml
target:
  - agent: codex
  - agent: claude
  - agent: kimi
```

Each entry accepts an optional `path`. The single-target mapping remains supported. Targets run in list order;
a failure stops the remaining targets. Earlier successful targets remain applied, with their own backups.

With `source: local` or `source` omitted, `path` selects a local directory relative to the manifest. It must contain
`SKILL.md` with matching `name` and a non-empty `description` in its YAML frontmatter. Codex installs into
`~/.agents/skills`; Claude installs into `$CLAUDE_CONFIG_DIR/skills` (default `~/.claude/skills`);
Kimi installs into `$KIMI_CODE_HOME/skills`.

Claude reserves the name `synced`; targets inside `.trash`, synced catalogs, or symlinked paths produce conflicts.

For a Git skill, set `source` to the repository and `path` to the skill directory within it:

```yaml
skills:
  - name: code-review
    source: example/skills
    path: skills/code-review
    ref: main
```

Supported source forms:

| Source | Meaning |
| ------ | ------- |
| `local` | Local skill directory selected by `path` |
| `example/skills` | GitHub repository shorthand |
| `github:example/skills` | Explicit GitHub shorthand |
| `github.com/example/skills` | GitHub repository without a URL scheme |
| `https://github.com/example/skills.git` | HTTPS Git repository, on GitHub or another host |
| `ssh://git@example.com/skills.git` or `git@example.com:skills.git` | SSH Git repository |
| `git://example.com/skills.git` | Git protocol repository |
| `git+https://example.com/skills.git` or `git+ssh://git@example.com/skills.git` | Explicit Git URL |

`source` accepts `local`, repository URLs, and GitHub shorthand. Local filesystem paths and `file://` URLs are rejected.
For a local skill, set `source: local` or omit `source`, and set `path`; `ref` requires a repository source. The `git` and
`github` fields are not accepted.

Git must be installed for repository sources. Existing Git authentication is used, with terminal credential prompts
disabled. For repository sources, `path` defaults to the repository root, and `ref` defaults to the remote's `HEAD`;
branches, tags, and full commit hashes are supported.
Repository paths must remain inside the repository. Bundles must satisfy the same frontmatter rules as local skills;
symlinks and submodules are rejected.

Both preview and apply fetch the selected revision into a temporary bare repository, then read its Git archive into
memory. Archive attributes such as `export-ignore` apply. Acquisition has a two-minute timeout and the temporary
repository is removed before loading returns. No checkout, hooks, submodule updates, or skill scripts run. Each loaded
snapshot is reused across all targets during that invocation.

When `target.path` is omitted, the agent's environment variable and default home are used:

| Agent | Environment variable | Default        |
| ----- | -------------------- | -------------- |
| Codex | `CODEX_HOME`         | `~/.codex`     |
| Claude | `CLAUDE_CONFIG_DIR` | `~/.claude` |
| Kimi  | `KIMI_CODE_HOME`     | `~/.kimi-code` |

Set `target.path` to an absolute path or one starting with `~/` to override the agent home. Codex's shared skill
location remains under the real home directory.

Preview or apply the default manifest, or select another file:

```bash
skillet
skillet apply
skillet --file /path/to/skillet.yaml
```

Planning leaves agent files unchanged. Applying takes a lock, rechecks the plan, retains backups under the selected
agent's `.skillet/backups` directory, and replaces only declared skill bundles. Undeclared skills are left alone.

The planned full manifest format is documented in [`skillet.schema.yaml`](skillet.schema.yaml).

## License

[MIT](LICENSE)
