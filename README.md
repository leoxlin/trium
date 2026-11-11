# skillets

<div align="center">
  <img src="skillets.png" alt="skillets" width="250">
  <br>
  An easy way to manage my agent skills — and more.
</div>

## Tracer bullet

The CLI reconciles local and Git-sourced skills and plugins, plus MCP server configurations, for Codex, Claude Code,
and Kimi Code.

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
For a local skill, set `source: local` or omit `source`, and set `path`; `ref` requires a repository source.
The `git` and `github` fields are not accepted.

Git must be installed for repository sources. Existing Git authentication is used, with terminal credential prompts
disabled. For repository sources, `path` defaults to the repository root, and `ref` defaults to the remote's `HEAD`;
branches, tags, and full commit hashes are supported.
Repository paths must remain inside the repository. Skill bundles must satisfy the same frontmatter rules as local
skills; symlinks and submodules are rejected.

Both preview and apply fetch the selected revision into a temporary bare repository, then read its Git archive into
memory. Archive attributes such as `export-ignore` apply. Acquisition has a two-minute timeout and the temporary
repository is removed before source preparation returns. No checkout, hooks, submodule updates, or skill scripts run.
Each loaded snapshot is reused across all targets during that invocation.

Plugins use the same source, path, and ref fields as skills:

```yaml
plugins:
  - name: team-tools
    source: local
    path: plugins/team-tools
```

A plugin is a complete native bundle. It does not require a root `SKILL.md`, and its embedded skills remain inside
that plugin. The native manifest must contain a matching name (or Kimi ID) for every selected target:

| Agent | Required native manifest |
| ----- | ------------------------ |
| Codex | `.codex-plugin/plugin.json` |
| Claude | `.claude-plugin/plugin.json` |
| Kimi | `kimi.plugin.json` or `.kimi-plugin/plugin.json` |

Applying installs and enables plugins. Codex and Claude use a local `skillet` marketplace under the selected agent home;
Kimi uses its native local installation registry. Managed sources live under `<agent-home>/plugins/managed/<name>`.
Codex also receives a native versioned cache copy. Older Codex cache versions are retained when versions change.
Plugin code, hooks, and MCP servers may run in later agent sessions; Skillet does not execute them during installation.
Restart or reload the agent to pick up changes.

Only local installations at Skillet's managed paths are eligible for updates. Matching plugins from other marketplaces
or scopes, missing managed bundles, duplicate registrations, and unsafe paths block apply before skill or plugin
writes. Unrelated native settings and plugins are preserved. A skill and plugin can share a name without sharing
installed files.

MCP servers are declared directly, without a bundle source:

```yaml
mcps:
  - name: filesystem
    command: npx
    args: [-y, "@modelcontextprotocol/server-filesystem", /srv/files]
    env:
      LOG_LEVEL: info
  - name: docs
    url: https://mcp.example.com/mcp
    headers:
      X-Client: skillet
    bearer_token_env: DOCS_TOKEN
```

Each server requires exactly one of `command` (stdio) or `url` (Streamable HTTP). Stdio accepts `args`, `env`, and
`cwd`; HTTP accepts `headers` and `bearer_token_env`. An Authorization header cannot accompany `bearer_token_env`.
A relative `cwd` resolves against the manifest directory. Claude does not support `cwd`; use a wrapper command for
servers that require a particular working directory. Other command arguments and environment values are passed through.

| Agent | User MCP configuration |
| ----- | ---------------------- |
| Codex | `<agent-home>/config.toml`, under `mcp_servers` |
| Claude | `~/.claude.json`, under `mcpServers` |
| Kimi | `<agent-home>/mcp.json`, under `mcpServers` |

For Claude, setting `CLAUDE_CONFIG_DIR` or `target.path` places `.claude.json` inside that agent home instead.
When using `target.path`, launch Claude with the same `CLAUDE_CONFIG_DIR` to load that configuration.
Bearer token references stay in native configuration and are resolved by the agent at runtime; Skillet never reads
the token. Claude uses `Authorization: Bearer ${DOCS_TOKEN}`; Codex and Kimi use their native token-variable fields.

Applying reconciles declared connection fields, preserves native server options such as tool policy and timeouts,
and enables Codex and Kimi entries. Claude's existing approval and disable policies remain in effect.
Undeclared servers and unrelated settings remain. Servers are never launched or contacted by Skillet;
restart or reload the agent to use the configuration. Native configuration rewrites can change formatting and comments;
backups retain the original bytes.

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
skillet /path/to/skillet.yaml
skillet apply /path/to/skillet.yaml
```

Planning leaves agent files unchanged. Applying takes a per-user lock at `~/.skillet/apply.lock`, rechecks the plan,
retains backups under the selected agent's `.skillet/backups` directory, and replaces declared skill and plugin bundles
and updates declared MCP connections. The lock covers all selected agents, including shared skill destinations; its file
remains after unlocking. Undeclared resources are left alone. Each resource class restores its own affected bundles and
registry files if writing or verification fails. Earlier successful resource classes remain applied.

The manifest format is documented in [`skillet.schema.yaml`](skillet.schema.yaml).

## License

[MIT](LICENSE)
