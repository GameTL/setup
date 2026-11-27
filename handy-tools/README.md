# handy-tools

Small zero-dependency Go TUIs I use day to day.

## Install / Update

Requires Go (`go` 1.22+).

```bash
curl -fsSL https://raw.githubusercontent.com/GameTL/setup/main/handy-tools/install.sh | bash
```

What it does:
1. Checks the latest commit on `main` against what you already have installed in `~/.local/bin` (`not installed`, `update available`, or `up to date`).
2. Opens an interactive spacebar picker (new & outdated tools pre-selected; up-to-date tools unchecked).
3. Installs selected tools to `~/.local/bin` and adds `~/.local/bin` to `PATH` in `~/.zshrc` (zsh) or `~/.bashrc` (bash) if needed.

Skip the picker and install specific tools directly:

```bash
curl -fsSL https://raw.githubusercontent.com/GameTL/setup/main/handy-tools/install.sh | bash -s -- gitprune
```

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/GameTL/setup/main/handy-tools/uninstall.sh | bash
```

Checks what's actually installed first, shows an interactive spacebar picker for installed tools, removes the selected binaries, and cleans up the `# handy-tools` PATH line once none remain.

## Tools

### `gitprune`

Interactive picker for cleaning up local git branches and worktrees in the current repo. Groups branches by merge/PR state (`merged`, `merged · worktree has changes`, `merged PR · new commits since`, `fresh worktree · no commits yet`, `open PR`, `not merged`, `in use · can't prune`).

- **Keys:** `j/k` move, `h/l` fold group, `e` open/close all, `space` pick, `a` merged only, `n` none, `p` prune (`y` to confirm), `q` quit
- **Flags:** `-base <branch>` (default: `origin/HEAD`), `-no-fetch`
- **Optional:** `gh` CLI for PR status and clickable PR links

### `diskpurge`

Interactive picker for reclaiming disk space from caches and build leftovers (Trash, Go, Rust/Cargo debug targets, npm/npx, Homebrew, `uv`, `~/Library/Caches`, Electron/Chromium app caches).

- **Keys:** `j/k` move, `h/l` fold group, `e` open/close all, `space` pick, `a` all, `n` none, `d`/`p` purge (`y` to confirm), `q` quit
- **Flags:** `-dry-run`, `-no-sudo`
