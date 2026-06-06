# weft

Composable AI rules manager — manage, layer, and sync AI rule sources across teams and harnesses.

Maintain separate rule repositories (personal, team, company) and compose them into a single layered profile applied to whichever AI coding tool you're using. Weft normalises across harnesses automatically — the same source writes `CLAUDE.md` for Claude Code, `AGENTS.md` for Codex, `GEMINI.md` for Gemini CLI, and a `.mdc` rule for Cursor.

Sources can use a flat `CLAUDE.md` or a full domain hierarchy (`Backend/BACKEND.md`, `Backend/Java/JAVA.md`, …). Set `instruction_glob: "**/*.md"` in the source config and Weft assembles every matching file — parent directories before children — before merging and applying.

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

> **`make dev` is not the same as watch mode.**  
> `make dev` is for *developing weft* — it rebuilds the binary on Go source changes.
> To use weft's write-back / live-reload feature, run `weft profile use <profile> --watch`
> in a separate terminal. If you want both at once (editing Go source while testing watch
> mode), pass the args through air:
> ```bash
> make dev ARGS="profile use tech --watch"
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

# Pull latest from all remotes
weft source sync

# Combine sources into a named profile
weft profile create hybrid --sources personal,work

# Activate the profile (merges sources, writes to harness config)
weft profile use hybrid

# Keep the profile live — re-applies automatically when any source file changes
weft profile use hybrid --watch

# Apply to a specific harness
weft target apply claude-code

# Verify everything is configured correctly
weft doctor
```

## Safe apply — manifest and backups

When weft writes files to a harness config directory it keeps a manifest
(`~/.config/weft/manifests/<harness>.json`) that records the sha256 hash of
every file it wrote. On the next apply:

- **File not on disk** — written silently.
- **File exists, hash matches manifest** — weft owns it, overwritten silently.
- **File exists, hash differs** — externally modified; weft backs it up to
  `~/.config/weft/backups/<harness>/<timestamp>/` (preserving the full
  relative path structure), writes the new content, and prints which files
  were backed up.

Files weft has never touched (e.g. `~/.claude/projects/`) are never modified.

```
  ! 2 file(s) externally modified — backed up to ~/.config/weft/backups/claude-code/20260605-143022
      CLAUDE.md
      commands/backend/java.md
```

To restore a backup:

```bash
weft target revert claude-code                    # restore latest backup
weft target revert claude-code --backup 20260605-143022  # restore a specific one
weft target backups claude-code                   # list all available backups
```

## Commands

| Command | Description |
|---|---|
| `source add <name> <path>` | Register a source; remote inferred from repo origin or set with `--remote` |
| `source list/status/remove` | List, inspect git state, or deregister sources |
| `source sync [name]` | Pull latest from remotes (auto-synced in background; use to force immediately) |
| `source push <name>` | Push commits; aborts if working tree is dirty — use `-m` to commit first |
| `source push <name> -m "msg"` | Stage all changes, commit with message, then push |
| `profile create/list/use/diff/inspect/delete` | Manage named profiles; `--overlay`, `--target`, and `--sources` are validated on create |
| `profile use <name> --watch` | Activate profile with live reload on source file changes |
| `target list/apply/backups/revert` | Manage AI harness targets; inspect and restore backups |
| `hook add/list/run/remove` | Manage lifecycle hooks |
| `doctor` | Health check — shows discovered harnesses and config issues |
| `version` | Print version, commit, and build date |

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

## Configuration

Config file: `~/.config/weft/config.yaml`

Override with `--config <path>` on any command.

## License

MIT
