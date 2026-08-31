#!/usr/bin/env bash
# General setup for Game — Fedora Bazzite (rpm-ostree, immutable)
# Each block below is standalone — copy/paste one at a time, or run the whole file.
# GitHub username used below: gametl — change if that's not right.

# ============================================================
# BLOCK 1 — Switch shell to zsh
# ============================================================
if ! command -v zsh &>/dev/null; then
  brew install zsh
fi
sudo chsh -s "$(command -v zsh)" "$USER"
# Log out/in (or start a new terminal) for the shell change to take effect.


# ============================================================
# BLOCK 2 — oh-my-zsh + .zshrc alias
# ============================================================
NONINTERACTIVE=1 sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)"
echo "alias zshrc='vim ~/.zshrc'" >> ~/.zshrc


# ============================================================
# BLOCK 3 — CLI tools via Homebrew
# rpm-ostree layering would need a reboot to take effect; brew doesn't.
# ============================================================
if ! command -v brew &>/dev/null; then
  echo "Homebrew not found — installing..."
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
fi
brew install vim neovim curl btop bat speedtest


# ============================================================
# BLOCK 4 — SSH server, pubkey-only auth
# openssh-server already ships in the Bazzite base image — just enable it.
# ============================================================
sudo systemctl enable --now sshd

sudo sed -i -E \
  's/^#?(PasswordAuthentication|KbdInteractiveAuthentication|ChallengeResponseAuthentication).*/\1 no/; s/^#?PubkeyAuthentication.*/PubkeyAuthentication yes/' \
  /etc/ssh/sshd_config /etc/ssh/sshd_config.d/*.conf 2>/dev/null || true

sudo sshd -t && sudo systemctl restart sshd

# Pull public key from GitHub (public keys are meant to be public — safe to script)
mkdir -p ~/.ssh && chmod 700 ~/.ssh
curl -fsSL https://github.com/gametl.keys >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys


# ============================================================
# BLOCK 5 — Tailscale
# Already built into Bazzite images — no curl-install needed.
# ============================================================
GREEN='\033[0;32m'
NC='\033[0m'

echo -e "${GREEN}==> Enabling Tailscale via ujust...${NC}"
ujust enable-tailscale

echo -e "${GREEN}==> Starting Tailscale sign-in — this will print a URL (or open a browser).${NC}"
echo -e "${GREEN}==> Complete the login there; this script will wait and continue automatically once it's done.${NC}"
sudo tailscale up --operator="$USER"

# tailscale up blocks until auth finishes, but double-check status before moving on
until sudo tailscale status &>/dev/null; do
  echo -e "${GREEN}==> Still waiting on Tailscale auth...${NC}"
  sleep 2
done
echo -e "${GREEN}==> Tailscale is up. Continuing...${NC}"


# ============================================================
# BLOCK 6 — journald log cleanup
# ============================================================
sudo journalctl --vacuum-time=30d   # keep only the last month of logs
sudo journalctl --vacuum-size=10G   # cap total log disk usage at 10G


# ============================================================
# BLOCK 7 — No more password for sudo
# ============================================================
echo "${USER} ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/"${USER}"-nopasswd
sudo chmod 0440 /etc/sudoers.d/"${USER}"-nopasswd
sudo visudo -cf /etc/sudoers.d/"${USER}"-nopasswd
