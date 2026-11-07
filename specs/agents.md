# Native adapters

Codex, Claude Code, and Kimi Code are private adapters inside `internal/reconcile`. Each validates acquired plugin
metadata without target I/O and plans native changes from explicit paths. Shared skill handling, plugin planning,
transactions, and locking remain in the same module.

## Target resolution

The CLI reads environment variables and supplies absolute `Homes` values. A manifest target override takes precedence;
`~/` expands against the supplied user home. Native adapters never read the process environment.

| Agent | Environment variable | Default agent home | Skill root |
| --- | --- | --- | --- |
| Codex | `CODEX_HOME` | `~/.codex` | `~/.agents/skills` |
| Claude | `CLAUDE_CONFIG_DIR` | `~/.claude` | `<agent-home>/skills` |
| Kimi | `KIMI_CODE_HOME` | `~/.kimi-code` | `<agent-home>/skills` |

Codex's shared skill location remains tied to the user home when its agent home is overridden. Kimi and Claude skill
locations follow their selected agent homes. The common apply lock is `<user-home>/.skillet/apply.lock`; backups remain
under each selected agent home's `.skillet/backups` directory.

## Skills

All agents install complete directory bundles and compare canonical content fingerprints. Target links, non-directory
bundles, links within bundles, and symlinked ancestors prevent installation.

- Codex observes `config.toml` and conflicts on a disabled entry for the requested `SKILL.md` path. That configuration
  belongs to the skill batch's read set even though skill installation does not modify it.
- Claude conflicts on reserved `synced` and `.trash` locations and symlinked catalogs before scanning their content.
- Kimi installs directory-form skills in its own skill root.

## Plugins

Plugin content validation is separate from standalone skill frontmatter. Each selected agent requires a matching name
or Kimi ID in its native manifest. Codex validates the version used in its cache path before native scanning.

| Agent | Native manifest | Managed bundle |
| --- | --- | --- |
| Codex | `.codex-plugin/plugin.json` | `<agent-home>/plugins/managed/<name>` |
| Claude | `.claude-plugin/plugin.json` | `<agent-home>/plugins/managed/<name>` |
| Kimi | `kimi.plugin.json` or `.kimi-plugin/plugin.json` | `<agent-home>/plugins/managed/<name>` |

The current implementation uses direct native-file operations:

- Codex writes `plugins/.agents/plugins/marketplace.json`, `[marketplaces.skillet]`, and `[plugins."<name>@skillet"]` in
  `config.toml`. It also installs `plugins/cache/skillet/<name>/<version>`. Older cache versions remain.
- Claude writes `plugins/managed/.claude-plugin/marketplace.json`, `plugins/known_marketplaces.json`, version-2
  `plugins/installed_plugins.json`, and `settings.json`'s `enabledPlugins`. User-scope IDs are `<name>@skillet`.
- Kimi writes version-1 `plugins/installed.json` with `source: "local-path"`, a managed root, and enabled state.

A private per-plugin record groups the intended path, cache copies, installed state, and conflict. Native adapters own
registry interpretation; shared planning owns bundle comparisons and replacement construction.

Only registrations at Skillet's managed local paths are eligible for update. External sources, scopes, duplicates,
missing bundles, orphan bundles, and unsafe paths block apply. Equal bundle content with disabled native state or a
changed cache requires an update. Plugins are enabled for later agent sessions; installation executes no plugin content.

## MCP servers

`mcps.go` plans a separate native-file batch after skills and plugins. Codex uses `config.toml`'s `mcp_servers`;
Kimi uses `mcp.json`'s `mcpServers`. Claude uses user-scope `mcpServers` in `<user-home>/.claude.json`, or
`<agent-home>/.claude.json` when its configuration directory or target path is explicitly selected.
The CLI supplies the explicit Claude state path through `Homes.ClaudeConfig` when `CLAUDE_CONFIG_DIR` is set.

Stdio maps command, arguments, environment, and working directory. Claude rejects working directories during
preparation. HTTP maps URLs and headers; token references use Codex's `bearer_token_env_var`, Kimi's
`bearerTokenEnvVar`, and Claude's Authorization header with a native environment reference. Skillet never resolves token
values. Declared connection fields are replaced, including stale transport fields, while unrelated native server options
remain. Codex and Kimi entries are enabled; Claude's separate approval and disable policies are preserved.

Native format references: [Codex](https://developers.openai.com/codex/mcp),
[Claude](https://code.claude.com/docs/en/mcp), and
[Kimi](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/mcp).

## Native documents

JSON decoding preserves unknown fields and numeric values and rejects duplicate keys, multiple documents, and non-object
roots. TOML decoding preserves unrelated values and rejects invalid native syntax. Unsupported registry versions or
shapes fail explicitly. Rewriting can change whitespace and comments; original bytes remain in backups.

Read sets contain raw native-file digests and canonical bundle digests. A plugin batch includes its managed bundle,
cache copies, and registry writes in one rollback operation. Post-write verification uses a fresh native plan.
