# Schema

The default file is `skillet.yaml`. YAML was chosen for a human-maintained manifest; decoding must be strict. Version 1
uses local or public GitHub bundle sources for skills and plugins, plus portable native configuration fields.

## Document shape

```yaml
version: 1

skills:
  - name: review-pr
    source: ./skills/review-pr

  - name: remote-review
    github: leoxlin/skillets
    path: skills/review-pr
    ref: v1.3.0

plugins:
  - name: team-workflows
    github: example/team-workflows
    ref: 0123456789abcdef0123456789abcdef01234567

mcps:
  - name: context7
    command: npx
    args: ["-y", "@upstash/context7-mcp"]
    env:
      LOG_LEVEL: warn

  - name: internal-docs
    url: https://mcp.example.com/mcp
    headers:
      X-Region: us-east-1
    bearer_token_env: INTERNAL_DOCS_TOKEN

context:
  - name: working-agreements
    source: ./context/working-agreements.md
```

Every resource class is optional. An omitted class and an empty list are equivalent and produce no changes for that
class.

## Common rules

- `version` is required and must be the integer `1`. Other versions fail before agent discovery.
- Unknown top-level or resource fields, duplicate YAML keys, and duplicate names within a class are errors. The same
  name may be used in different classes.
- Names are case-sensitive and must match `[a-z0-9][a-z0-9._-]{0,63}`. Agent adapters may reject additional reserved
  names.
- Manifest order is retained only when appending new context segments. Identity and all other change ordering use the
  resource name.
- Local source paths are slash-separated, relative to the skillet file, and must remain inside its directory after path
  cleaning. Absolute paths, `..` escapes, URLs, and symlinks in local source paths are invalid.
- A source's identity is its content, not its location. Moving an unchanged local source or changing a GitHub ref
  without changing its content does not produce an update.
- Null values are not accepted; omit optional fields instead. Optional maps and lists must have their documented types,
  and YAML scalar coercion is not used to infer strings.

## GitHub bundle sources

Skills and plugins declare exactly one of `source` or `github`. A GitHub source has these fields:

| Field    | Type   | Required | Meaning                                                                            |
| -------- | ------ | -------- | ---------------------------------------------------------------------------------- |
| `github` | string | yes      | Public repository as `owner/repo`; URLs, credentials, queries, and fragments fail  |
| `path`   | string | no       | Slash-separated bundle directory inside the repository; defaults to its root       |
| `ref`    | string | no       | Branch, tag, or commit to acquire; defaults to the repository's default branch     |

When present, `path` is a non-empty relative directory and must remain inside the repository after cleaning; `.` selects
the root. Absolute paths, `..` escapes, backslashes, and symlinked paths are invalid. `path` and `ref` are invalid with
a local `source`. A `ref` may contain slashes but cannot be empty or contain whitespace or control characters. Pinning
a full commit SHA makes a manifest reproducible; a branch or tag can resolve to different content on a later invocation.

Version 1 supports anonymous HTTPS access to public GitHub repositories only. Private repositories, other Git hosts,
GitHub CLI dependencies, credential helpers, submodules, and persistent download caches are outside its scope.

## `skills`

A skill installs one directory-form Agent Skill from a local or GitHub bundle.

| Field                | Type               | Required | Meaning                                                        |
| -------------------- | ------------------ | -------- | -------------------------------------------------------------- |
| `name`               | name string        | yes      | Portable skill and installed directory name                    |
| `source` or `github` | source fields      | yes      | Exactly one local directory or GitHub repository declaration   |

Validation requires a regular `SKILL.md` with YAML frontmatter containing `name` and `description`. The frontmatter
`name` must equal the manifest name. Flat single-file skills are not managed in version 1.

The semantic fingerprint is SHA-256 over a deterministic stream of each regular file's slash-separated relative path,
bytes, and executable bit. Modification times, ownership, empty directories, and non-executable permission bits do not
participate. Repository, path, ref, and resolved commit do not participate. Symlinks and special files are invalid. The
same bundle algorithm is used for local and GitHub sources, and for plugins.

## `plugins`

A plugin installs and enables one local or GitHub native plugin bundle.

| Field                | Type            | Required | Meaning                                                      |
| -------------------- | --------------- | -------- | ------------------------------------------------------------ |
| `name`               | name string     | yes      | Portable plugin identity                                     |
| `source` or `github` | source fields   | yes      | Exactly one local directory or GitHub repository declaration |

The generic loader requires a directory and computes its bundle fingerprint. Each agent adapter then validates its own
manifest and requires the native plugin name or ID to equal `name`. A bundle may carry manifests for more than one
agent.

A plugin is one logical resource even though installation can update both a managed bundle and an installation/enabled
registry. Its semantic fingerprint combines the bundle fingerprint with the required enabled state, so a disabled local
plugin produces an update even when its content is unchanged. Plugin-bundled skills, MCPs, and hooks remain opaque to
the standalone resource adapters.

Plugins installed from a marketplace, synchronized account, enterprise policy, or another externally controlled source
are conflicts when their ID matches a desired plugin. Skillet updates only native local installations; acquiring the
replacement bundle from GitHub does not make an externally managed installation eligible for update.

## `mcps`

Each entry declares exactly one stdio or streamable HTTP server. The presence of `command` selects stdio; the presence
of `url` selects HTTP.

- `name` (name string, required for both transports): MCP server identity.
- `command` (non-empty string, required for stdio): executable or launch command.
- `args` (list of strings, optional for stdio): literal argument vector; defaults to empty.
- `env` (map of string to string, optional for stdio): literal child-process environment overlay.
- `cwd` (non-empty string, optional for stdio): native working directory; not resolved by Skillet.
- `url` (absolute HTTP(S) URL, required for HTTP): streamable HTTP endpoint.
- `headers` (map of string to string, optional for HTTP): literal static headers.
- `bearer_token_env` (environment variable name, optional for HTTP): name of the variable containing a bearer token.

`command` and `url` are mutually exclusive. Fields marked for one transport are invalid with the other. An empty `env`,
`args`, or `headers` value is canonicalized to the corresponding omitted value. No field is expanded: in particular,
Skillet does not substitute environment variables, resolve `cwd`, or contact the URL.

For semantic comparison, adapters map native spellings into this domain model, sort map keys, and use defaults for
omitted empty fields. The fingerprint excludes unrelated native settings. When an update changes transport, the adapter
removes obsolete fields from the old transport while preserving unrelated native fields.

Version 1 deliberately excludes SSE, WebSocket, OAuth client configuration, remote environment IDs, tool policies,
timeout controls, and enable/disable state. New entries use the agent's default enabled state. Existing native fields in
these excluded categories, including an explicitly disabled state, are unmanaged and preserved.

## `context`

A context entry manages one named segment in the agent's native global instruction file.

| Field    | Type                   | Required | Meaning                                                                                 |
| -------- | ---------------------- | -------- | --------------------------------------------------------------------------------------- |
| `name`   | name string            | yes      | Marker and segment identity                                                             |
| `source` | relative Markdown file | yes      | UTF-8 segment body                                                                      |

The installed form is:

```markdown
<!-- skillet:begin working-agreements -->

...canonical source content...

<!-- skillet:end working-agreements -->
```

Source content is normalized to LF line endings. Non-empty content has exactly one trailing newline; empty content
remains empty. It must be valid UTF-8 and must not contain `skillet:begin` or `skillet:end`. Empty content installs an
empty marked segment.

On scan, the content between a marker pair is canonicalized the same way before fingerprinting. New segments are
appended in manifest order with one blank line separating them from surrounding text. Existing segments stay in place
when updated. Text outside marker pairs is not reformatted.

Missing markers, duplicate pairs for one name, mismatched pairs, or nested Skillet markers are conflicts. A segment
removed from the manifest remains installed and unmanaged by subsequent plans.

## Validation timing

Validation has two layers:

1. **Manifest validation** runs before agent discovery and covers document shape, common fields, source declarations,
   acquired bundle content, and source fingerprints. Static declaration errors prevent all acquisition. GitHub HTTP and
   archive failures are operational errors; successfully acquired bundles receive the same validation as local ones.
2. **Adapter validation** runs during planning and covers native manifests, reserved names, native capabilities, target
   conflicts, external ownership, and native format constraints.

An adapter must never silently drop an unsupported requested field or coerce it into a different meaning.
