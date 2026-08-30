# weft

[![ci](https://github.com/jophira/weft/actions/workflows/ci.yml/badge.svg)](https://github.com/jophira/weft/actions/workflows/ci.yml)
[![go](https://img.shields.io/github/go-mod/go-version/jophira/weft)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

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

**Build from source** (requires Go 1.26.6+, matching the `go` directive in `go.mod`)
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

# See which harnesses are installed, and target the ones the profile misses
weft target detect
weft target detect --add

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
- **File unchanged** (hash matches manifest) — left as-is, no write needed (`· unchanged`).
- **File externally modified** — written back to its source repo first, then apply is a no-op.
- **Unresolvable file** (no owning source) — backed up to
  `~/.config/weft/backups/<harness>/<timestamp>/` with a warning; apply skips it.

Files weft has never touched (e.g. `~/.claude/projects/`) are never modified.

The manifest separates two things: `files` is the durable record of everything weft has
written for that harness, and `staged` is the subset the active profile currently projects.
Ownership therefore survives a profile switch — a file that leaves the profile and comes
back later is still recognised as weft's own output rather than mistaken for a hand edit.

When a file drops out of the active profile, weft cleans it up rather than orphaning it:

- **Still matches the manifest hash** — deleted from the target (`− removed`), and any
  directory it emptied is pruned.
- **Edited since weft wrote it** — left exactly as-is with a warning (`! kept`). Weft will
  not delete work it has no claim over; the file simply stops being managed.

```
[weft] startup write-back: CLAUDE.md → pers-tech
Applying to claude-code...
  · unchanged CLAUDE.md
  ✓ wrote     commands/backend/java.md
  − removed   skills/old-skill/SKILL.md
  ! kept      commands/tweaked.md (edited since weft wrote it — no longer managed)
```

To restore a backup (last-resort cases only):

```bash
weft target revert claude-code                    # restore latest backup
weft target revert claude-code --backup 20260605-143022  # restore a specific one
weft target backups claude-code                   # list all available backups
```

## Adoption — bringing harness-native files under weft

Write-back only fires for files a source already owns. A file you author directly inside a
harness — a new `~/.claude/agents/reviewer.md` — belongs to no source, so it never reaches
any other harness. `weft adopt` closes that gap.

```bash
weft adopt --scan                                          # list files no source owns
weft adopt claude-code agents/reviewer.md --into pers-tech # copy it into a source
```

`--scan` walks every harness weft projects to and lists the files no manifest owns, grouped
by harness and class. Only `commands`, `agents` and `skills` are adoptable, and only
markdown — harness runtime state (`projects/`, `todos/`, caches, `.git`) is never offered.

Adoption is **explicit and one-way**. Once a source owns the file, weft overwrites it in
every harness on each apply, so nothing is adopted implicitly and never on a watcher tick.
The guards:

- **Confirmation.** The plan (every source → destination pair) is printed and confirmed
  before anything is written. `--yes` skips the prompt for scripted use.
- **No ambiguity.** With more than one source in the profile, `--into` is required; weft
  lists the candidates rather than guessing which repo the file belongs in.
- **No clobbering.** A destination that already exists in the source is refused by name;
  `--force` overrides.
- **No secrets.** A file carrying what looks like a literal credential (`sk-ant-`, `ghp_`,
  `AKIA`, a long high-entropy token, …) is refused outright. Sources are ordinary git repos
  that get pushed, so this guard is not bypassable by `--force` — externalise the value as
  `${env:NAME}` and adopt again.

Adopting also records weft's ownership of the harness copy in the manifest, so the next
apply reports `· unchanged` rather than treating the file as an external edit.

## Coverage — what weft manages, and what it does not

`weft target list --files` audits every detected harness against its config root:

```
claude-code  ~/.claude
  ✓ managed
      CLAUDE.md  instructions            root instruction file
      agents/    agents        1 file    subagent definitions
      commands/  commands      18 files  slash commands
      skills/    skills        20 files  skill bundles
  ~ unmanaged
      hooks/                 hooks       4 files    hook scripts
      plugins/               plugins     467 files  installed plugins
      settings.json          settings               hooks, permissions, env, status line
      settings.local.json    settings               machine-local settings overrides
      statusline-command.sh  statusline             status line command
  · other: 1 unrecognised entry
```

**Managed** is what weft writes, per its manifest. **Unmanaged** is what weft recognises and
leaves alone, which is the half worth acting on. **Other** counts entries weft does not
recognise at all; `--all` names them. Credentials files are never listed.

A harness weft knows no layout for says so, rather than reporting empty coverage. The two
are different answers and only one of them is a gap in weft.

`weft project status` asks the same question of the current repository, showing which
instruction files are read as input and which weft writes.

## Class-aware projection — what each harness gets

A source holds more than instructions. Commands, agents, skills and MCP server definitions
all live alongside them, and no two harnesses read them from the same place. Claude Code
runs commands from `~/.claude/commands/`, Codex from `~/.codex/prompts/`, and Cursor runs
none at all.

So weft routes by **class** rather than copying the staged tree verbatim. Every file is one
of `instructions`, `commands`, `agents`, `skills` or `mcp`, and each harness declares what
it does with each one:

| Class | claude-code | codex | cursor | gemini-cli |
|---|---|---|---|---|
| `instructions` | `CLAUDE.md` | `AGENTS.md` | `weft.mdc` | `GEMINI.md` |
| `commands` | `commands/` | `prompts/` | advertised | advertised |
| `agents` | `agents/` | advertised | advertised | advertised |
| `skills` | `skills/` | advertised | advertised | advertised |
| `mcp` | `~/.claude.json` | `~/.codex/config.toml` | `~/.cursor/mcp.json` | `~/.gemini/settings.json` |

Codex runs markdown prompts, so commands translate by relocation alone. Gemini CLI's
commands are TOML rather than markdown, which is a format gap and not just a path gap, so
they are advertised until a TOML emitter exists. Cursor consumes `.mdc` rule files and
nothing else.

A class with no native home is **skipped and logged**, never written to a path the tool
ignores. One line per class per apply:

```
  ~ skipped   4 agents file(s) — advertised in the instruction index instead
  ~ skipped   2 skills file(s) — no native location in this harness
```

Some skipped classes are still **advertised**: the harness cannot execute them, but it can
read a file when told one exists, so weft appends an index to the managed instruction block
naming what is available and where. It widens reach cheaply and is not parity, so it is
never reported as such.

Classes withheld by config are reported separately, because "this harness cannot take it"
and "you told weft not to send it" have different fixes:

```
  ~ skipped   6 commands file(s) — excluded by harness_sync config
```

`instructions` and `mcp` never travel as copied files. The instruction file is a managed
block inside a document whose surrounding prose is yours, and MCP config is merged key by
key into a file holding unrelated tool state, so `~/.claude.json` keeps everything that is
not an `mcpServers` entry. Canonical MCP lives in a source's `mcp.yaml` and each harness
renders its own dialect. Secrets go in by reference only (`${env:GITHUB_TOKEN}`), never as
a literal.

A harness weft knows nothing about, including anything you add through `harnesses.yaml`,
copies every class at its staged path. That is wrong for a tool with its own layout, but
weft has no basis to guess a better one, and dropping the files silently would be worse than
putting them where you can find them.

### Choosing what participates — `harness_sync`

Set it per profile to narrow what a harness receives:

```yaml
harness_sync:
  claude-code: [instructions, commands, agents, skills, mcp]
  codex:       [instructions, commands, mcp]
  cursor:      [instructions]
```

A harness with no entry is unrestricted and gets every class it natively supports, so a
profile written before `harness_sync` existed projects exactly what it did before. An
explicit empty list projects nothing.

## Conflicts — two harnesses, one file

Write-back takes an edit made inside one harness, pushes it to the owning source, and the
next apply fans it out to the others. That is safe while only one copy has moved. When two
harnesses have both been edited since the last apply, whichever one write-back visits last
would win, and the other edit would be gone with nothing left to recover it from.

So weft refuses. Both copies stay on disk exactly as you left them, the apply holds the file
rather than overwriting either side, and it reports:

```
! conflict: commands/review.md changed in claude-code and codex since 14:02
  → weft resolve commands/review.md --take claude-code|codex
```

Nothing is written until you name a winner:

```bash
weft resolve commands/review.md --take claude-code
```

The losing copies are backed up under `~/.config/weft/backups/resolve/<timestamp>/` before
they are rewritten, and the owning source is updated so the next apply keeps the decision
instead of undoing it. Nothing is deleted.

### Merging instead of choosing

`--take` settles a conflict by discarding one side. Most of the time nothing needed
discarding, because the two edits were in different places. `--take merge` keeps both:

```bash
weft resolve commands/review.md --take merge
```

Weft merges every diverged copy against what it last wrote, then opens the result in
`$EDITOR`. The saved file is the resolution. Nothing reaches a source or a harness until
the editor closes, and quitting without saving leaves the conflict held.

The review is not skippable, and least skippable when the merge comes out clean. Two
harnesses can each add a rule, in different places, that contradict each other. The text
merges without a marker, weft reports nothing, and the model reads both on the next turn.
A merge with markers already has your attention; a clean one is the one that slips past.

That makes merging interactive by definition. `--take merge` without a terminal fails and
tells you why, while `--take <harness>` stays fully non-interactive for scripts. If
`$EDITOR` is unset, weft writes the merge, prints its path, and prints the
`weft resolve ... --merged <file>` command that applies it once you have read it.

A review can sit open, or sit on disk, for as long as you like, so weft re-checks the
conflict before applying one. If a harness has written to the same file in the meantime,
the resolution is refused rather than applied over that edit, and your review is kept where
it is so you can merge again against what is there now.

Markers never reach a harness. These files are live model input, so a `<<<<<<<` block in
`~/.codex/AGENTS.md` is read as instructions on the next turn. Conflicted text goes to a
work file under `~/.config/weft/merge/` only, and a saved merge that still carries markers
is refused.

### Walking every conflict

`weft resolve` with no arguments walks the held conflicts one at a time:

```
! conflict 1/2: instructions:pers-tech
    changed in codex and windsurf since 14:02

  [m] merge both, and review in $EDITOR
  [c] take codex
  [w] take windsurf
  [d] show the diff
  [s] skip
  [q] quit
```

Letters bind to harness names, since the set of harnesses varies. Each choice does exactly
what the equivalent flag does. Skipping leaves that conflict held and moves on, so you can
settle two now and one tomorrow; quitting leaves the remainder held.

Interactive is sugar on top of the flags. The loop is what you get when stdin is a terminal
and no `--take` was given. With `--yes`, or with stdin redirected, weft reports what is held
and exits non-zero rather than asking, because apply also runs under the watcher, where a
question would hang with nobody to answer it.

### What is covered

Conflict detection covers `commands`, `agents` and `skills` as whole files, and Tier B
instruction blocks per source section, named `instructions:<source>`:

```
! conflict: instructions (pers-tech) changed in codex and windsurf since 09:12
  → weft resolve instructions:pers-tech --take codex|windsurf
```

Sections are the unit rather than the whole block, so two harnesses editing different
sources in the same window do not collide. For files, the path you pass is the one printed
in the report, relative to the staged tree, which is the one name that means the same thing
in every harness.

## Status line

`weft status --short` prints a single line for a shell prompt or a harness status line:

```
weft: hybrid · 3 harness · drift:1 · adopt:18 · conflict:2 · watch:on
```

`adopt` is the count `weft adopt --scan` would list, and `conflict` the number of files
being held. Both come from a cache the last apply wrote, so the line costs a file read
rather than a walk of every harness root, which matters when it renders once per turn. They
are left out entirely when no apply has recorded them, or when what was recorded is more
than a day old: a stale number sends you looking for files that have since moved.

For Claude Code, wire it up as the `statusLine` command in `~/.claude/settings.json`. Codex
has no per-turn shell hook, so this is a Claude-family affordance rather than something
every harness can show.

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
| `target detect [--add]` | Report which harnesses are installed, the signal each was found by, and whether the active profile targets them; `--add` appends the new ones to the profile (never removes) |
| `adopt --scan` | List commands/agents/skills authored inside a harness that no source owns |
| `adopt <harness> <path>... --into <source>` | Copy those files into a source so weft can fan them out; confirms first, refuses to clobber (`--force`) or to carry literal credentials |
| `hook add/list/run/remove` | Manage lifecycle hooks |
| `resolve <target-path>` | Reverse-lookup the source(s) that produced a file written to a harness |
| `resolve <path> --take <harness>` | Settle a held conflict by taking one harness's copy; backs the losing copies up first and updates the owning source |
| `resolve <path> --take merge` | Merge every diverged copy and open the result in `$EDITOR`; the saved file is the resolution, and quitting without saving leaves the conflict held |
| `resolve` | Walk every held conflict interactively; `--yes` or a non-terminal reports and exits non-zero instead |
| `status [--short]` | Show active profile and per-harness projection state (instruction path, block drift), plus the cached adoptable and conflict counts |
| `autostart enable/disable/status` | Opt in to running the watcher at login (systemd user unit, LaunchAgent, or Task Scheduler); `--profile` pins a profile, `--linger` keeps it alive without a login session |
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

## Autostart — keep the watcher running across reboots

`weft profile use` stays in the foreground and dies with the terminal that
started it, so after a reboot weft is silently not running and sources drift
from targets. `weft autostart` installs a per-user service that starts the
watcher at login.

It is strictly opt-in — no other command installs a background service.

```bash
weft autostart enable                  # follow the active profile
weft autostart enable --profile work   # pin one profile regardless of last use
weft autostart status                  # installed? running? which binary/profile?
weft autostart disable                 # stop and remove — leaves no orphan unit
```

| OS | Mechanism | Installed at |
|---|---|---|
| Linux | systemd **user** unit, `Restart=on-failure` | `~/.config/systemd/user/weft.service` |
| macOS | LaunchAgent, `KeepAlive` on unclean exit | `~/Library/LaunchAgents/com.jophira.weft.plist` |
| Windows | Task Scheduler task at logon, hidden | task named `weft` |

**Profile selection.** By default the unit resolves `active_profile` from
`config.yaml` each time it starts, so the machine comes back up on whatever
profile was last switched to. `--profile <name>` pins it instead, for machines
that should always boot into a known state.

**Switching profiles needs nothing extra.** `weft profile use <other>` detects
the autostarted watcher's singleton lock and hands the profile off — the running
watcher hot-swaps in place. There is never a second watcher, and the UX is
identical whether the watcher was autostarted or launched by hand.

**Linux and logout.** Without `loginctl enable-linger`, systemd stops user
services when your last session ends. Pass `--linger` to opt into that (it
changes machine-wide policy for your user, so weft never enables it silently).

**Re-pointing after an upgrade.** The unit hardcodes a binary path. If that
binary moves, `weft autostart status` reports it as stale; re-running
`weft autostart enable` overwrites the unit with the current path.

### Surfacing "is weft running?" in your prompt

`weft status --short` prints one line in ~16 ms and exits 0 even with no config
at all, which makes it a good fit for a shell prompt or a harness status line:

```
weft: hybrid · 3 harness · drift:0 · watch:on
```

Wire it into Claude Code (`~/.claude/settings.json`):

```json
{
  "statusLine": { "type": "command", "command": "weft status --short" }
}
```

Or into a shell prompt:

```bash
PS1='$(weft status --short) \w $ '   # bash
```

`watch:off` is the signal that the watcher is not running. This is deliberately
*not* a session-start hook that prints a warning: hook stdout is injected into
the model's context on every session, so a banner would cost tokens forever to
report a condition that is usually fine.

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

### Aider

Aider has no default conventions path, so writing the file is not enough on its
own. Weft adds a `read` entry pointing at it to `~/.aider.conf.yml`, merging into
that file and leaving every other key, and your comments, untouched.

Aider looks for `.aider.conf.yml` in the git root, then the working directory,
then your home directory, and uses the first one it finds rather than merging
them. A repo with its own `.aider.conf.yml` therefore does not see the entry weft
writes. Add a `read` entry to the project config too when you want the profile
loaded there.

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
`warn_instruction_size_kb`, `harness_home`, `log_level`, `log_max_kb`,
`log_generations`, `advice_muted`, `advice_throttle_hours`, `project_sync`,
`project_max_age_days`, `project_write`.

Override the config location with `--config <path>` on any command. It isolates
the state weft owns (sources, profiles, staged output, manifests, backups) under
that file's directory.

### Isolating what weft writes to

`--config` moves weft's own state. It does not move where an apply **writes**:
harness directories are resolved from your home directory, so `weft profile use`
still updates the real `~/.claude`, `~/.codex` and the rest.

To redirect those as well, set `harness_home` (or `WEFT_HARNESS_HOME`):

```bash
WEFT_HARNESS_HOME=/tmp/weft-test weft --config /tmp/weft-test/config.yaml profile use demo
```

Every harness path then resolves under that directory: detection, config roots,
instruction files and MCP documents. Weft's own log, update check and git
credentials keep using your real home, because those are not harness state.

This is what makes it safe to exercise an apply on a machine that also runs weft
for real. Without it, weft warns on any `--config` run that harness writes are
still going to your actual home.

## Changelog

[CHANGELOG.md](CHANGELOG.md) covers every release. Each section is what the
release notes for that tag are built from.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for local setup, the test tiers and the
commit convention. Security reports go through GitHub private vulnerability
reporting, see [SECURITY.md](SECURITY.md).

## License

MIT. See [LICENSE](LICENSE).
