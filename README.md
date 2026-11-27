# setup
Game Personal Bazzite bootstrap script — zsh, oh-my-zsh, CLI tools, SSH (pubkey-only),
Tailscale, journald cleanup, and passwordless sudo.

## Run it with one command

```bash
curl -fsSL https://raw.githubusercontent.com/GameTL/setup/main/bazzite.sh | bash
```

This pulls the script straight from the repo and runs it top to bottom. The
Tailscale step is interactive — it'll print a sign-in URL and pause there
until you finish auth in the browser, then continue on its own.

### Run it block by block instead

If you'd rather step through it (e.g. on a new machine you're not fully sure
about yet), download it first and run pieces manually:

```bash
curl -fsSL https://raw.githubusercontent.com/GameTL/setup/main/bazzite.sh -o bazzite.sh
chmod +x bazzite.sh
less bazzite.sh   # skim it before running anything piped from the internet
```

Then copy/paste individual `BLOCK` sections into your terminal, or run the
whole thing with `./bazzite.sh`.

## What it does

1. Switches the default shell to `zsh`
2. Installs oh-my-zsh and adds a `zshrc` alias for editing `~/.zshrc`
3. Installs `vim`, `neovim`, `curl`, `btop`, `bat`, `speedtest` via Homebrew
4. Enables `sshd`, restricts SSH to pubkey-only auth, and pulls the public
   key from `github.com/GameTL.keys`
5. Enables Tailscale (`ujust enable-tailscale`) and brings it up
6. Trims journald logs (30-day retention, 10G cap)
7. Enables passwordless `sudo` for the current user

## Notes

- Assumes Fedora **Bazzite** (rpm-ostree/immutable) — CLI tools go through
  Homebrew rather than `rpm-ostree install` so nothing needs a reboot.
- Piping a script from `curl` straight into `bash` means you're trusting
  the repo at time of run — fine for your own public repo, but worth a
  quick skim (`curl -fsSL ... | less`) before running on a new box.
- The SSH pubkey pull uses GitHub's `.keys` endpoint, which only ever
  exposes public keys — safe to use in a public repo.

## Other tools

- [`handy-tools/`](handy-tools/) — standalone Go TUIs (`gitprune`, `diskpurge`) with one-line install/uninstall scripts.
