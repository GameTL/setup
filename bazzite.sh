#!/usr/bin/env bash
# General setup for Game — Fedora Bazzite (rpm-ostree, immutable)
# Each BLOCK is isolated — copy from that block's GREEN= through its end, paste in terminal.
# Or run the whole file: bash bazzite.sh
# GitHub username used below: gametl — change if that's not right.


# ============================================================
# BLOCK 0 — Bootstrap Homebrew
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

setup_brew() {
  if [[ -x /home/linuxbrew/.linuxbrew/bin/brew ]]; then
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  elif command -v brew &>/dev/null; then
    eval "$(brew shellenv)"
  fi
}

ensure_brew() {
  setup_brew
  if ! command -v brew &>/dev/null; then
    echo "Homebrew not found — installing..."
    NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  fi
}

echo -e "${GREEN}==> Block 0: bootstrapping Homebrew...${NC}"
ensure_brew


# ============================================================
# BLOCK 1 — Install zsh and set it as default shell
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

setup_brew() {
  if [[ -x /home/linuxbrew/.linuxbrew/bin/brew ]]; then
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  elif command -v brew &>/dev/null; then
    eval "$(brew shellenv)"
  fi
}

ensure_brew() {
  setup_brew
  if ! command -v brew &>/dev/null; then
    echo "Homebrew not found — installing..."
    NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  fi
}

find_zsh() {
  setup_brew
  local zsh_path=""
  zsh_path="$(command -v zsh 2>/dev/null || true)"
  if [[ -z "$zsh_path" && -x /home/linuxbrew/.linuxbrew/bin/zsh ]]; then
    zsh_path="/home/linuxbrew/.linuxbrew/bin/zsh"
  fi
  if [[ -z "$zsh_path" ]]; then
    echo "ERROR: zsh not found. Run Block 1 first, or: brew install zsh"
    return 1
  fi
  export PATH="$(dirname "$zsh_path"):$PATH"
  echo "$zsh_path"
}

echo -e "${GREEN}==> Block 1: installing zsh and setting it as default shell...${NC}"
ensure_brew

if ! command -v zsh &>/dev/null; then
  brew install zsh
fi

ZSH_PATH="$(find_zsh)"
grep -qxF "$ZSH_PATH" /etc/shells 2>/dev/null || echo "$ZSH_PATH" | sudo tee -a /etc/shells >/dev/null

sudo usermod --shell "$ZSH_PATH" "$USER"
sudo usermod --shell "$ZSH_PATH" root

echo -e "${GREEN}==> Default shell set to ${ZSH_PATH}. Open a new terminal or run: exec zsh${NC}"


# ============================================================
# BLOCK 2 — Set root password
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

echo -e "${GREEN}==> Block 2: setting the root password (you'll be prompted)...${NC}"
sudo passwd root


# ============================================================
# BLOCK 3 — Optionally rename the host
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

CURRENT_HOSTNAME="$(hostname)"
echo -e "${GREEN}==> Block 3: current hostname is '${CURRENT_HOSTNAME}'.${NC}"
read -r -p "Change the hostname? (y/N): " CHANGE_HOSTNAME
if [[ "$CHANGE_HOSTNAME" =~ ^[Yy]$ ]]; then
  read -r -p "Enter new hostname: " NEW_HOSTNAME
  sudo hostnamectl set-hostname "$NEW_HOSTNAME"
  echo -e "${GREEN}==> Hostname changed to '${NEW_HOSTNAME}'.${NC}"
else
  echo -e "${GREEN}==> Keeping hostname as '${CURRENT_HOSTNAME}'.${NC}"
fi


# ============================================================
# BLOCK 4 — oh-my-zsh + .zshrc alias
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

setup_brew() {
  if [[ -x /home/linuxbrew/.linuxbrew/bin/brew ]]; then
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  elif command -v brew &>/dev/null; then
    eval "$(brew shellenv)"
  fi
}

find_zsh() {
  setup_brew
  local zsh_path=""
  zsh_path="$(command -v zsh 2>/dev/null || true)"
  if [[ -z "$zsh_path" && -x /home/linuxbrew/.linuxbrew/bin/zsh ]]; then
    zsh_path="/home/linuxbrew/.linuxbrew/bin/zsh"
  fi
  if [[ -z "$zsh_path" ]]; then
    echo "ERROR: zsh not found. Run Block 1 first, or: brew install zsh"
    return 1
  fi
  export PATH="$(dirname "$zsh_path"):$PATH"
  echo "$zsh_path"
}

echo -e "${GREEN}==> Block 4: installing oh-my-zsh and adding zshrc alias...${NC}"
ZSH_PATH="$(find_zsh)"

if [[ -d "$HOME/.oh-my-zsh" ]]; then
  echo "oh-my-zsh already installed — skipping clone."
else
  RUNZSH=no CHSH=no KEEP_ZSHRC=yes \
    sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)"
fi

grep -qxF "alias zshrc='vim ~/.zshrc'" ~/.zshrc 2>/dev/null || \
  echo "alias zshrc='vim ~/.zshrc'" >> ~/.zshrc


# ============================================================
# BLOCK 5 — Powerlevel10k theme + configuration
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

setup_brew() {
  if [[ -x /home/linuxbrew/.linuxbrew/bin/brew ]]; then
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  elif command -v brew &>/dev/null; then
    eval "$(brew shellenv)"
  fi
}

find_zsh() {
  setup_brew
  local zsh_path=""
  zsh_path="$(command -v zsh 2>/dev/null || true)"
  if [[ -z "$zsh_path" && -x /home/linuxbrew/.linuxbrew/bin/zsh ]]; then
    zsh_path="/home/linuxbrew/.linuxbrew/bin/zsh"
  fi
  if [[ -z "$zsh_path" ]]; then
    echo "ERROR: zsh not found. Run Block 1 first, or: brew install zsh"
    return 1
  fi
  export PATH="$(dirname "$zsh_path"):$PATH"
  echo "$zsh_path"
}

echo -e "${GREEN}==> Block 5: installing Powerlevel10k theme...${NC}"
ZSH_CUSTOM="${ZSH_CUSTOM:-$HOME/.oh-my-zsh/custom}"
if [[ ! -d "$ZSH_CUSTOM/themes/powerlevel10k" ]]; then
  git clone --depth=1 https://github.com/romkatv/powerlevel10k.git \
    "$ZSH_CUSTOM/themes/powerlevel10k"
fi

if grep -q 'powerlevel10k/powerlevel10k' ~/.zshrc; then
  sed -i 's/^ZSH_THEME=.*/ZSH_THEME="powerlevel10k\/powerlevel10k"/' ~/.zshrc
else
  sed -i 's/^ZSH_THEME=.*/ZSH_THEME="powerlevel10k\/powerlevel10k"/' ~/.zshrc 2>/dev/null || \
    echo 'ZSH_THEME="powerlevel10k/powerlevel10k"' >> ~/.zshrc
fi

echo -e "${GREEN}==> Block 5: running p10k configure wizard...${NC}"
ZSH_PATH="$(find_zsh)"
"$ZSH_PATH" -i -c 'p10k configure'


# ============================================================
# BLOCK 6 — CLI tools via Homebrew
# rpm-ostree layering would need a reboot; brew is live immediately.
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

setup_brew() {
  if [[ -x /home/linuxbrew/.linuxbrew/bin/brew ]]; then
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  elif command -v brew &>/dev/null; then
    eval "$(brew shellenv)"
  fi
}

ensure_brew() {
  setup_brew
  if ! command -v brew &>/dev/null; then
    echo "Homebrew not found — installing..."
    NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
  fi
}

echo -e "${GREEN}==> Block 6: installing CLI tools via Homebrew...${NC}"
ensure_brew
brew install vim neovim curl btop bat speedtest-cli git


# ============================================================
# BLOCK 7 — SSH server, pubkey-only auth
# openssh-server already ships in the Bazzite base image — just enable it.
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

echo -e "${GREEN}==> Block 7: enabling sshd and switching to pubkey-only auth...${NC}"
sudo systemctl enable --now sshd

sudo tee /etc/ssh/sshd_config.d/99-pubkey-only.conf >/dev/null <<'EOF'
PasswordAuthentication no
KbdInteractiveAuthentication no
ChallengeResponseAuthentication no
PubkeyAuthentication yes
EOF

sudo sshd -t && sudo systemctl restart sshd

echo -e "${GREEN}==> Fetching public key from github.com/gametl.keys...${NC}"
mkdir -p ~/.ssh && chmod 700 ~/.ssh
touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys
curl -fsSL https://github.com/gametl.keys >> ~/.ssh/authorized_keys
sort -u ~/.ssh/authorized_keys -o ~/.ssh/authorized_keys


# ============================================================
# BLOCK 8 — Tailscale
# Already built into Bazzite — ujust enable-tailscale, not curl-install.
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

echo -e "${GREEN}==> Block 8: enabling Tailscale via ujust...${NC}"
ujust enable-tailscale

echo -e "${GREEN}==> Starting Tailscale sign-in — this will print a URL (or open a browser).${NC}"
echo -e "${GREEN}==> Complete the login there; this script waits until auth finishes.${NC}"
sudo tailscale up --operator="$USER"

until sudo tailscale status &>/dev/null; do
  echo -e "${GREEN}==> Still waiting on Tailscale auth...${NC}"
  sleep 2
done
echo -e "${GREEN}==> Tailscale is up. Continuing...${NC}"


# ============================================================
# BLOCK 9 — journald log cleanup
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

echo -e "${GREEN}==> Block 9: trimming journald logs...${NC}"
sudo journalctl --vacuum-time=30d   # keep only the last month of logs
sudo journalctl --vacuum-size=10G   # cap total log disk usage at 10G


# ============================================================
# BLOCK 10 — Passwordless sudo
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

echo -e "${GREEN}==> Block 10: enabling passwordless sudo for ${USER}...${NC}"
echo "${USER} ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/"${USER}"-nopasswd
sudo chmod 0440 /etc/sudoers.d/"${USER}"-nopasswd
sudo visudo -cf /etc/sudoers.d/"${USER}"-nopasswd


# ============================================================
# BLOCK 11 — Power: no suspend/hibernate, display off after 1h
# ============================================================
set -euo pipefail
GREEN='\033[0;32m'
NC='\033[0m'

DISPLAY_SLEEP_SEC=3600

echo -e "${GREEN}==> Block 11: disabling suspend/hibernate; display sleep ${DISPLAY_SLEEP_SEC}s (1h)...${NC}"

# systemd — block sleep/hibernate targets
sudo mkdir -p /etc/systemd/sleep.conf.d
sudo tee /etc/systemd/sleep.conf.d/no-suspend-hibernate.conf >/dev/null <<'EOF'
[Sleep]
AllowSuspend=no
AllowHibernation=no
AllowSuspendThenHibernate=no
AllowHybridSleep=no
EOF

sudo systemctl mask sleep.target suspend.target hibernate.target \
  hybrid-sleep.target suspend-then-hibernate.target 2>/dev/null || true

# logind — ignore suspend keys / lid; no idle suspend at login manager level
sudo mkdir -p /etc/systemd/logind.conf.d
sudo tee /etc/systemd/logind.conf.d/no-suspend.conf >/dev/null <<'EOF'
[Login]
HandleSuspendKey=ignore
HandleHibernateKey=ignore
HandleLidSwitch=ignore
HandleLidSwitchExternalPower=ignore
HandleLidSwitchDocked=ignore
IdleAction=ignore
EOF

sudo systemctl restart systemd-logind

# GNOME user session — blank display after 1h, never suspend/hibernate
if command -v gsettings &>/dev/null; then
  gsettings set org.gnome.desktop.session idle-delay "$DISPLAY_SLEEP_SEC"
  gsettings set org.gnome.settings-daemon.plugins.power sleep-inactive-ac-type 'blank'
  gsettings set org.gnome.settings-daemon.plugins.power sleep-inactive-battery-type 'blank'
  gsettings set org.gnome.settings-daemon.plugins.power sleep-inactive-ac-timeout "$DISPLAY_SLEEP_SEC"
  gsettings set org.gnome.settings-daemon.plugins.power sleep-inactive-battery-timeout "$DISPLAY_SLEEP_SEC"
fi

# GDM login screen — same policy when nobody is logged in
if [[ -d /etc/dconf/db ]]; then
  sudo mkdir -p /etc/dconf/db/gdm.d
  sudo tee /etc/dconf/db/gdm.d/00-power.conf >/dev/null <<EOF
[org/gnome/desktop/session]
idle-delay=${DISPLAY_SLEEP_SEC}

[org/gnome/settings-daemon/plugins/power]
sleep-inactive-ac-type='blank'
sleep-inactive-battery-type='blank'
sleep-inactive-ac-timeout=${DISPLAY_SLEEP_SEC}
sleep-inactive-battery-timeout=${DISPLAY_SLEEP_SEC}
EOF
  sudo dconf update
fi

echo -e "${GREEN}==> All blocks complete. Run: exec zsh${NC}"
