#!/usr/bin/env bash
set -euo pipefail

REPO_URL="https://github.com/GameTL/setup.git"
MODULE="github.com/GameTL/setup/handy-tools"
REF="${HANDY_TOOLS_REF:-main}"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
STATE_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/handy-tools"

ALL_TOOLS=(gitprune diskpurge)
ALL_DESCS=(
  "interactive git branch & worktree pruner"
  "interactive cache & build-leftover purger"
)

if ! command -v go >/dev/null 2>&1; then
  echo "handy-tools: 'go' is required (https://go.dev/dl/)" >&2
  exit 1
fi

# Resolve latest commit SHA on REF (git ls-remote avoids GitHub API rate limits).
resolve_remote_sha() {
  local sha=""
  if command -v git >/dev/null 2>&1; then
    sha=$( (git ls-remote "$REPO_URL" "refs/heads/$REF" 2>/dev/null || true) | awk 'NR==1 {print $1}' )
  fi
  if [ -z "$sha" ] && command -v curl >/dev/null 2>&1; then
    sha=$( (curl -fsSL "https://api.github.com/repos/GameTL/setup/commits/$REF" 2>/dev/null || true) \
      | sed -n 's/^[[:space:]]*"sha":[[:space:]]*"\([0-9a-f]\{40\}\)".*/\1/p' | head -n 1 )
  fi
  printf '%s' "$sha"
}

installed_sha() {
  local tool="$1"
  local bin="$BIN_DIR/$tool"
  [ -x "$bin" ] || return 0
  if [ -f "$STATE_DIR/$tool.sha" ]; then
    head -n 1 "$STATE_DIR/$tool.sha"
    return 0
  fi
  # Fallback: extract short commit from Go module pseudo-version embedded in the binary.
  (go version -m "$bin" 2>/dev/null || true) \
    | awk '$1 == "mod" && $2 == "github.com/GameTL/setup" { n = split($3, a, "-"); if (n >= 3) print a[n] }'
}

same_commit() {
  local a="$1" b="$2"
  [ -n "$a" ] && [ -n "$b" ] || return 1
  case "$b" in
    "$a"*) return 0 ;;
  esac
  case "$a" in
    "$b"*) return 0 ;;
  esac
  return 1
}

short_sha() {
  printf '%.7s' "$1"
}

echo "Checking latest commit on $REF ..."
REMOTE_SHA=$(resolve_remote_sha)
if [ -n "$REMOTE_SHA" ]; then
  REMOTE_SHORT=$(short_sha "$REMOTE_SHA")
  INSTALL_VER="$REMOTE_SHA"
else
  REMOTE_SHORT="$REF"
  INSTALL_VER="$REF"
fi

# Inspect current state for each tool.
N=${#ALL_TOOLS[@]}
SELECTED=()
STATUS_TEXT=()
STATUS_KIND=() # new | update | current

for ((i = 0; i < N; i++)); do
  SELECTED[i]=0
  tool="${ALL_TOOLS[i]}"
  cur=$(installed_sha "$tool")
  if [ ! -x "$BIN_DIR/$tool" ]; then
    STATUS_KIND[i]="new"
    STATUS_TEXT[i]="not installed -> $REMOTE_SHORT"
  elif [ -n "$REMOTE_SHA" ] && same_commit "$cur" "$REMOTE_SHA"; then
    STATUS_KIND[i]="current"
    STATUS_TEXT[i]="up to date ($REMOTE_SHORT)"
  elif [ -n "$cur" ]; then
    STATUS_KIND[i]="update"
    STATUS_TEXT[i]="update available ($(short_sha "$cur") -> $REMOTE_SHORT)"
  else
    STATUS_KIND[i]="update"
    STATUS_TEXT[i]="installed (unknown commit) -> $REMOTE_SHORT"
  fi
done

# If tool names were passed as args, select only those and skip the picker.
if [ "$#" -gt 0 ]; then
  for ((i = 0; i < N; i++)); do
    SELECTED[i]=0
  done
  for arg in "$@"; do
    found=0
    for ((i = 0; i < N; i++)); do
      if [ "${ALL_TOOLS[i]}" = "$arg" ]; then
        SELECTED[i]=1
        found=1
        break
      fi
    done
    if [ "$found" -eq 0 ]; then
      echo "handy-tools: unknown tool '$arg' (available: ${ALL_TOOLS[*]})" >&2
      exit 1
    fi
  done
elif [ -r /dev/tty ] && [ -w /dev/tty ]; then
  # Interactive spacebar picker (compatible with bash 3.2+ and curl | bash).
  TTY=/dev/tty
  C_RESET=$'\033[0m'
  C_BOLD=$'\033[1m'
  C_DIM=$'\033[2m'
  C_GREEN=$'\033[32m'
  C_YELLOW=$'\033[33m'
  C_CYAN=$'\033[36m'

  cleanup_tty() {
    printf '\033[?25h' >"$TTY" 2>/dev/null || true
  }
  trap cleanup_tty EXIT INT TERM

  cursor=0
  menu_lines=$((N + 3))

  draw_menu() {
    printf '\033[K%sSelect tools to install/update%s %s(origin/%s @ %s)%s\n' \
      "$C_BOLD" "$C_RESET" "$C_DIM" "$REF" "$REMOTE_SHORT" "$C_RESET" >"$TTY"
    for ((i = 0; i < N; i++)); do
      local ptr="  " box="[ ]" scol="$C_DIM"
      if [ "$i" -eq "$cursor" ]; then ptr="${C_CYAN}> ${C_RESET}"; fi
      if [ "${SELECTED[$i]}" -eq 1 ]; then box="${C_GREEN}[x]${C_RESET}"; fi
      case "${STATUS_KIND[$i]}" in
        new)     scol="$C_CYAN" ;;
        update)  scol="$C_YELLOW" ;;
        current) scol="$C_GREEN" ;;
      esac
      printf '\033[K%s%s %s%-10s%s  %s%-40s%s  %s%s%s\n' \
        "$ptr" "$box" "$C_BOLD" "${ALL_TOOLS[$i]}" "$C_RESET" \
        "$scol" "${STATUS_TEXT[$i]}" "$C_RESET" \
        "$C_DIM" "${ALL_DESCS[$i]}" "$C_RESET" >"$TTY"
    done
    printf '\033[K\n' >"$TTY"
    printf '\033[K%sj/k or ↑/↓ move · space toggle · a all · n none · enter install · q quit%s\n' \
      "$C_DIM" "$C_RESET" >"$TTY"
  }

  printf '\033[?25l' >"$TTY"
  draw_menu

  while true; do
    IFS= read -rsn1 key <"$TTY" || break
    case "$key" in
      $'\x1b')
        IFS= read -rsn2 -t 1 rest <"$TTY" || rest=""
        case "$rest" in
          '[A'|'OA') cursor=$(( (cursor - 1 + N) % N )) ;;
          '[B'|'OB') cursor=$(( (cursor + 1) % N )) ;;
          '') printf '\nCancelled.\n' >"$TTY"; exit 0 ;;
        esac
        ;;
      k|K) cursor=$(( (cursor - 1 + N) % N )) ;;
      j|J) cursor=$(( (cursor + 1) % N )) ;;
      ' ') SELECTED[cursor]=$(( 1 - SELECTED[cursor] )) ;;
      a|A) for ((i = 0; i < N; i++)); do SELECTED[i]=1; done ;;
      n|N) for ((i = 0; i < N; i++)); do SELECTED[i]=0; done ;;
      q|Q) printf '\nCancelled.\n' >"$TTY"; exit 0 ;;
      '') break ;;
    esac
    printf '\033[%dA' "$menu_lines" >"$TTY"
    draw_menu
  done
  cleanup_tty
  trap - EXIT INT TERM
fi

to_install=()
for ((i = 0; i < N; i++)); do
  if [ "${SELECTED[$i]}" -eq 1 ]; then
    to_install+=("${ALL_TOOLS[$i]}")
  fi
done

if [ "${#to_install[@]}" -eq 0 ]; then
  echo "Nothing selected."
  exit 0
fi

mkdir -p "$BIN_DIR" "$STATE_DIR"

targets=()
for tool in "${to_install[@]}"; do
  targets+=("${MODULE}/${tool}@${INSTALL_VER}")
done

echo "Installing (${to_install[*]}) @ $REMOTE_SHORT -> $BIN_DIR ..."
GOPROXY="${HANDY_TOOLS_GOPROXY:-direct}" GONOSUMDB="github.com/GameTL/setup" GOBIN="$BIN_DIR" go install "${targets[@]}"

if [ -n "$REMOTE_SHA" ]; then
  for tool in "${to_install[@]}"; do
    printf '%s\n' "$REMOTE_SHA" > "$STATE_DIR/$tool.sha"
  done
fi

case "${SHELL##*/}" in
  zsh) rc="$HOME/.zshrc" ;;
  *)   rc="$HOME/.bashrc" ;;
esac

if ! grep -qF '.local/bin' "$rc" 2>/dev/null; then
  # shellcheck disable=SC2016
  printf '\nexport PATH="$HOME/.local/bin:$PATH" # handy-tools\n' >> "$rc"
  echo "Added \$HOME/.local/bin to PATH in $rc"
fi

echo "Done! Installed in $BIN_DIR:"
for tool in "${to_install[@]}"; do
  echo "  - $tool ($REMOTE_SHORT)"
done

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Run: source $rc   (or open a new terminal)" ;;
esac
