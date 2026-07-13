# weft

Composable AI rules manager — manage, layer, and sync AI rule sources across teams and harnesses.

Maintain separate rule repositories (personal, team, company) and compose them into a single layered profile applied to whichever AI coding tool you're using. Weft normalises across harnesses automatically — the same source writes `CLAUDE.md` for Claude Code, `AGENTS.md` for Codex, `GEMINI.md` for Gemini CLI, and a `.mdc` rule for Cursor.

Sources can use a flat `CLAUDE.md` or a full domain hierarchy (`Backend/BACKEND.md`, `Backend/Java/JAVA.md`, …). Set `instruction_glob: "**/*.md"` in the source config and Weft assembles every matching file — parent directories before children — before merging and applying.

For a **mixed-content source** (rules alongside tickets, docs, or knowledge dumps), pair the broad glob with `--instruction-exclude` to inline only a subset:

```bash
# assemble the always-on rules; leave language/ticket/doc trees on-disk and on-demand
weft source add work ~/.rules/work \
  --instruction-glob "**/*.md" \
  --instruction-exclude "java/,php/,tickets/,docs/"
```

Excludes are root-relative path prefixes, applied on top of the always-excluded managed dirs.

### Portable references — the `{{weft.root}}` anchor

Rule/command/agent files reference other files with anchors instead of hardcoded paths, so a source works wherever it's cloned:

- `{{weft.root}}` → this source's registered root
- `{{weft.source:NAME}}` → another source's root

`@{{weft.root}}/common/code-review.md` expands to a real absolute path on projection. Relocating a source is then just re-registering it — no file edits.

`weft doctor` lints your sources for stale, hardcoded, or broken path references and heals the safe ones:

```bash
weft doctor            # report actionable path references
weft doctor --fix      # rewrite hardcoded/stale paths to {{weft.root}} anchors
weft doctor --all      # also list external/dead references
```

It resolves stale prefixes by a unique trailing-path match inside your sources — so a reference to a file that was moved (or lives under a different root) is rewritten to the correct anchor automatically.

`weft doctor` also checks **rule-annotation health** so the convention-driven resolver never silently skips a source. It flags rule files missing front-matter (with a suggested `label:`/`detect:` derived from the path), sources that contribute nothing because none of their rules are annotated, duplicate labels (the resolver keeps the first and ignores the rest), and dangling `extends:` targets. Suggestions are printed for you to review and commit — documentation, project/ticket trees, and the source's own `CLAUDE.md` wrapper are never flagged.

Per-project rules are also supported: any directory in your source tree named `projects` or `project-rules` is automatically discovered. Weft lists every `.md` file found inside (recursively) as explicit paths in the assembled CLAUDE.md, grouped by project root so the AI can load the right rules for the active project.

## Install

**macOS / Linux — Homebrew**
```bash
brew install jophira/tap/weft
```

**Linux / macOS — binary**
```bash
curl -sSL https://github.com/jophira/weft/releases/latest/download/weft_linux_amd64.tar.gz | tar xz
sudo mv weft /usr/local/bin/
```

Replace `linux_amd64` with your platform: `linux_arm64`, `darwin_amd64`, `darwin_arm64`.

**Windows**

Download `weft_windows_amd64.zip` from the [releases page](https://github.com/jophira/weft/releases/latest), extract, and add to your `PATH`.

**Build from source** (requires Go 1.24+)
```bash
git clone https://github.com/jophira/weft.git && cd weft
make build        # binary at ./bin/weft
```

## Development

| Command | What it does |
|---|---|
| `make build` | Compile binary to `./bin/weft` |
| `make dev` | Run [`air`](https://github.com/air-verse/air) — rebuilds and restarts the binary whenever a `.go` file changes. For hacking on weft itself. |
| `make test` | Run the test suite (`go test ./...`) |
| `make lint` | Run `golangci-lint` |

> **`make dev` includes watch mode.**  
> It rebuilds the binary on Go source changes *and* runs `weft profile use <active-profile>`,
> which watches sources and targets by default. The active profile is read from
> `~/.config/weft/config.yaml` — set it once with `weft profile use <profile> --no-watch`,
> then `make dev` picks it up automatically on every restart.  
> To target a different profile without changing the active one:
> ```bash
> make dev ARGS="profile use other-profile"
> ```

## Quick start

```bash
# Register a rule source — remote is inferred from the repo's origin when omitted
weft source add work ~/.rules/work

# Specify the remote explicitly (or override an existing origin)
weft source add work ~/.rules/work --remote git@github.com:you/work-rules.git

# Register a source with a domain hierarchy (Backend/, Frontend/, …)
weft source add work-private ~/.rules/work-private \
  --instruction-glob "**/*.md"

# Register a source with custom project-rule directory names
weft source add work ~/.rules/work --project-dir-names "projects,project-rules,specs"

# Pull latest from all remotes
weft source sync

# Combine sources into a named profile
weft profile create hybrid --sources personal,work

# Activate the profile — merges sources, applies to all harnesses, and watches for changes
weft profile use hybrid

# One-shot apply only (no watch — useful in CI/scripts)
weft profile use hybrid --no-watch

# Apply to a specific harness
weft target apply claude-code

# Verify everything is configured correctly
weft doctor
```

## Per-project rules

Weft can inject per-project rule references into your assembled `CLAUDE.md` so the AI loads the right rules for whichever project you are working in.

### How it works

1. Place a marker in your source `CLAUDE.md` where the list should appear:
   ```
   <!-- weft:projects -->
   ```

2. Organise per-project rule files under a directory named `projects` or `project-rules` anywhere in your source tree. Both flat and nested layouts are supported:

   **Flat** — one file per project:
   ```
   php/project-rules/ubs-keyinvest.md
   java/project-rules/instrument-service.md
   ```

   **Nested** — one subdirectory per project (any depth):
   ```
   php/project-rules/ubs-keyinvest/ubs-keyinvest.md
   java/project-rules/instrument-service/instrument-service.md
   ```

3. On every `weft profile use`, the placeholder is replaced with a freshly generated block listing every `.md` file found, grouped by project root:

   ```
   <!-- weft:projects:begin — regenerated on every `weft profile use`; do not edit -->
   When working in a project, find the matching entry below and read its rule file(s):

   `~/rules/php/project-rules/`:
      - `~/rules/php/project-rules/ubs-keyinvest/ubs-keyinvest.md`

   `~/rules/java/project-rules/`:
      - `~/rules/java/project-rules/instrument-service/instrument-service.md`
   <!-- weft:projects:end -->
   ```

   The path naturally carries the language/category context (e.g. `php/`, `java/`). The AI matches entries by project name or technology and loads the appropriate file.

### Configuration

Weft searches for directory names `projects` and `project-rules` by default. To customise:

```bash
# Use additional names
weft source add work ~/.rules/work --project-dir-names "projects,project-rules,specs"
```

Or set `project_dir_names` directly in the source YAML:

```yaml
structure:
  project_dir_names:
    - projects
    - project-rules
    - specs
```

The legacy single-path `projects:` field is still honoured for backward compatibility.

## How weft writes instructions — priority layering & two-tier projection

Weft no longer dumps the full merged ruleset into a harness's global file. Instead it keeps its own
per-source copies and writes only a small managed block into each harness, so your global file stays
yours.

**Priority.** Give each source a `--priority` (higher wins). Weft assembles them lowest-first so the
highest-priority source is emitted last and takes precedence on conflict:

```bash
weft source add company ~/.rules/company --priority 30
weft source add team    ~/.rules/team    --priority 20
weft source add me      ~/.rules/me      --priority 10
```

Weft writes one copy per source to `~/.config/weft/profiles/<profile>/instructions/NN-<source>.md`
(`NN` = priority order) and projects them into each harness by its capability:

- **Import-capable (Tier A)** — Claude Code, Gemini CLI: the harness file gets only a managed block
  of import directives pointing at weft's copies, in priority order. Content stays out of your global
  file.
- **Single-file (Tier B)** — Codex, Windsurf, Aider, Cursor, and any unknown/user-defined harness:
  weft inlines the per-source content (with attribution markers) inside a `<!-- weft:begin/end -->`
  block. This is the default, so a new harness always works safely.

Everything **outside** the managed block is preserved byte-for-byte. Editing the inlined content in a
Tier B harness writes back to the owning source on the next apply, and re-projects to every harness —
so weft is the cross-harness sync hub.

Check the projection state at any time:

```bash
weft status            # active profile + per-harness profile, instruction path, block drift
weft status --short    # one line for a shell prompt / harness status line
```

## Safe apply — manifest, write-back and backups

Weft keeps a manifest (`~/.config/weft/manifests/<harness>.json`) recording the sha256 hash of
every file it wrote. On startup, before applying, it checks each managed file:

- **File not on disk** — written silently (`✓ wrote`).
- **File unchanged** (hash matches manifest) — skipped (`· skip`).
- **File externally modified** — written back to its source repo first, then apply is a no-op.
- **Unresolvable file** (no owning source) — backed up to
  `~/.config/weft/backups/<harness>/<timestamp>/` with a warning; apply skips it.

Files weft has never touched (e.g. `~/.claude/projects/`) are never modified.

```
[weft] startup write-back: CLAUDE.md → ai-rules-personal-tech
Applying to claude-code...
  · skip   CLAUDE.md
  ✓ wrote  commands/backend/java.md
```

To restore a backup (last-resort cases only):

```bash
weft target revert claude-code                    # restore latest backup
weft target revert claude-code --backup 20260605-143022  # restore a specific one
weft target backups claude-code                   # list all available backups
```

## Commands

| Command | Description |
|---|---|
| `source add <name> <path>` | Register a source; `--priority N` sets layering (higher wins); remote inferred from repo origin or set with `--remote` |
| `source list/status/remove` | List, inspect git state, or deregister sources |
| `source sync [name]` | Pull latest from remotes (auto-synced in background; use to force immediately) |
| `source push <name>` | Push commits; aborts if working tree is dirty — use `-m` to commit first |
| `source push <name> -m "msg"` | Stage all changes, commit with message, then push |
| `profile create/list/use/diff/inspect/delete` | Manage named profiles; `--overlay`, `--target`, and `--sources` are validated on create |
| `profile use <name>` | Activate profile: merge sources, apply to all targets, and watch for changes (use `--no-watch` to apply once and exit) |
| `target list/apply/backups/revert` | Manage AI harness targets; inspect and restore backups |
| `hook add/list/run/remove` | Manage lifecycle hooks |
| `status [--short]` | Show active profile and per-harness projection state (instruction path, block drift) |
| `doctor` | Health check — discovered harnesses, config issues, path-reference lint, and rule-annotation health (missing front-matter, duplicate labels, dangling extends, with suggested fixes); `--fix` heals stale/hardcoded paths to `{{weft.root}}` anchors, `--all` also lists external/dead refs |
| `version` | Print version, commit, and build date |
| `bug-report` | Print diagnostic bundle (version, environment, doctor, recent logs) for filing a GitHub issue |

## MCP server

`weft mcp serve` starts a [Model Context Protocol](https://modelcontextprotocol.io) server on stdio,
letting any MCP-aware agent (Claude Code, Cursor, Codex, …) introspect and control weft at runtime.

**Wire it into Claude Code** (`.claude/settings.json` or `~/.claude/settings.json`):

```json
{
  "mcpServers": {
    "weft": { "command": "weft", "args": ["mcp", "serve"] }
  }
}
```

### Tools

| Tool | What it does |
|---|---|
| `weft_profile_list` | List all profiles with sources, targets, and active status |
| `weft_profile_inspect` | Full detail for one profile |
| `weft_source_list` | List sources with basic git state |
| `weft_source_status` | Detailed git status for one source |
| `weft_source_sync` | Pull from remote (one source or all) |
| `weft_source_push` | Stage → commit → push; `message` param is required |
| `weft_doctor` | Health check: config dir, detected harnesses, target health |

### Resources

| URI | Content |
|---|---|
| `weft://profile/active` | Merged instruction text from the active profile |
| `weft://source/{name}/instructions` | Raw instruction content from a named source |
| `weft://harness/{name}/current` | What weft last wrote to a harness on disk |

Resources can be included in an agent's context at session start so it knows exactly which rules govern it. See the [MCP guide](https://github.com/jophira/weft/wiki) for end-to-end workflow examples.

## Supported harnesses

| Harness | Written as |
|---|---|
| Claude Code | `~/.claude/CLAUDE.md` |
| OpenAI Codex | `~/.codex/AGENTS.md` |
| Cursor | `~/.cursor/rules/weft.mdc` |
| Windsurf | `~/.codeium/windsurf/global_rules.md` |
| Gemini CLI | `~/.gemini/GEMINI.md` |
| Warp | `~/.warp/workflows/*.yaml` |
| Aider | `~/.aider/CONVENTIONS.md` |

Additional harnesses (Goose, OpenCode, Hermes, Antigravity) are supported via
plain directory copy. New harnesses can be added to `~/.config/weft/harnesses.yaml`
without recompiling.

## Home layout

Weft splits its state into two homes (see ADR 0003):

| Home | Holds |
|---|---|
| `~/weft/` (workbench) | `sources/<name>/` (source **content** repos), `templates/`, `docs/`, `work/` — what you author, edit, and share |
| `~/.config/weft/` (engine room) | `config.yaml`, `sources/*.yaml` (registry pointers), `profiles/*.yaml`, `staged/`, `hooks/`, `audit/` — bookkeeping weft manages |

A source has two parts: the **registry** (a tiny `<name>.yaml` pointer, engine-room)
and its **content** (the repo, workbench). The **work plane** (`~/weft/work/`) is
weft-owned: `projects/<repo>/` (per-repo knowledge base), `tickets/<TICKET>/`,
`plans/`, and `inbox/`.

```
weft init                 # scaffold the homes + templates (idempotent — safe to re-run)
weft source relocate <n>  # move a source's content into ~/weft/sources/<n> (registry repointed, bridged)
weft source rename <o> <n># rename a source AND rewrite every profile that references it
weft migrate              # relocate all registered sources' content into ~/weft (non-destructive)
weft migrate --docs       # also consolidate ~/docs under ~/weft/docs
weft docs adopt           # consolidate docs on its own
weft ticket new DIGI-123  # scaffold a ticket folder from ~/weft/templates
weft ticket list          # list scaffolded tickets
```

**Project knowledge auto-loads by repo identity.** `weft rules resolve` appends
every Markdown file under `~/weft/work/projects/<repo>/kb/` to the assembled rule
bundle, keyed by the repository's name — so a session hook feeds project context
into any harness with no per-harness wiring. Opt out with `--no-work`.

`weft migrate` moves content (never deletes), refuses to clobber a populated
destination, and leaves a symlink bridge at the old path so existing references
keep resolving.

### Path anchors

Sources reference other files with machine-independent tokens, expanded at
projection time:

| Token | Expands to |
|---|---|
| `{{weft.root}}` | the current source's root |
| `{{weft.source:NAME}}` | the root of source `NAME` |
| `{{weft.home}}` | the workbench root (`~/weft`) |
| `{{weft.docs}}` | the docs home (`~/docs`, or `~/weft/docs` after adopt) |

## Configuration

Config file: `~/.config/weft/config.yaml`. Keys: `weft_home`, `sources_dir`,
`profiles_dir`, `hooks_dir`, `docs_dir`, `audit_dir`, `active_profile`,
`warn_instruction_size_kb`.

Override the config location with `--config <path>` on any command — it fully
isolates weft's state (including `weft_home`) under that file's directory.

## License

MIT
