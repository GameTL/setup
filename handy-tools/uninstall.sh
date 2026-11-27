#!/usr/bin/env bash
set -euo pipefail

BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
STATE_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/handy-tools"
ALL_TOOLS=(gitprune diskpurge)

installed_sha() {
  local tool="$1"
  local bin="$BIN_DIR/$tool"
  if [ -f "$STATE_DIR/$tool.sha" ]; then
    head -n 1 "$STATE_DIR/$tool.sha"
    return 0
  fi
  if command -v go >/dev/null 2>&1; then
    (go version -m "$bin" 2>/dev/null || true) \
      | awk '$1 == "mod" && $2 == "github.com/GameTL/setup" { n = split($3, a, "-"); if (n >= 3) print a[n] }'
  fi
}

short_sha() {
  printf '%.7s' "$1"
}

has_rc_entry() {
  for rc in "$HOME/.zshrc" "$HOME/.bashrc"; do
    if [ -f "$rc" ] && grep -qF '# handy-tools' "$rc"; then
      return 0
    fi
  done
  return 1
}

# First check what is actually installed.
FOUND_TOOLS=()
FOUND_INFO=()
for tool in "${ALL_TOOLS[@]}"; do
  if [ -e "$BIN_DIR/$tool" ]; then
    sha=$(installed_sha "$tool")
    FOUND_TOOLS+=("$tool")
    if [ -n "$sha" ]; then
      FOUND_INFO+=("$BIN_DIR/$tool ($(short_sha "$sha"))")
    else
      FOUND_INFO+=("$BIN_DIR/$tool")
    fi
  fi
done

if [ "${#FOUND_TOOLS[@]}" -eq 0 ]; then
  if has_rc_entry; then
    echo "No handy-tools binaries found in $BIN_DIR, cleaning up PATH entry ..."
    for rc in "$HOME/.zshrc" "$HOME/.bashrc"; do
      if [ -f "$rc" ] && grep -qF '# handy-tools' "$rc"; then
        tmp=$(mktemp)
        grep -vF '# handy-tools' "$rc" > "$tmp" || true
        mv "$tmp" "$rc"
        echo "Removed handy-tools PATH entry from $rc"
      fi
    done
    rm -rf "$STATE_DIR"
  else
    echo "Nothing to uninstall (no handy-tools installed in $BIN_DIR)."
  fi
  exit 0
fi

N=${#FOUND_TOOLS[@]}
SELECTED=()
for ((i = 0; i < N; i++)); do
  SELECTED[i]=1
done

if [ "$#" -gt 0 ]; then
  for ((i = 0; i < N; i++)); do
    SELECTED[i]=0
  done
  for arg in "$@"; do
    found=0
    for ((i = 0; i < N; i++)); do
      if [ "${FOUND_TOOLS[i]}" = "$arg" ]; then
        SELECTED[i]=1
        found=1
        break
      fi
    done
    if [ "$found" -eq 0 ]; then
      echo "Not installed: $BIN_DIR/$arg"
    fi
  done
elif [ -r /dev/tty ] && [ -w /dev/tty ]; then
  TTY=/dev/tty
  C_RESET=$'\033[0m'
  C_BOLD=$'\033[1m'
  C_DIM=$'\033[2m'
  C_RED=$'\033[31m'
  C_CYAN=$'\033[36m'

  cleanup_tty() {
    printf '\033[?25h' >"$TTY" 2>/dev/null || true
  }
  trap cleanup_tty EXIT INT TERM

  cursor=0
  menu_lines=$((N + 3))

  draw_menu() {
    printf '\033[K%sInstalled handy-tools — select to uninstall%s\n' "$C_BOLD" "$C_RESET" >"$TTY"
    for ((i = 0; i < N; i++)); do
      local ptr="  " box="[ ]"
      if [ "$i" -eq "$cursor" ]; then ptr="${C_CYAN}> ${C_RESET}"; fi
      if [ "${SELECTED[$i]}" -eq 1 ]; then box="${C_RED}[x]${C_RESET}"; fi
      printf '\033[K%s%s %s%-10s%s  %s%s%s\n' \
        "$ptr" "$box" "$C_BOLD" "${FOUND_TOOLS[$i]}" "$C_RESET" \
        "$C_DIM" "${FOUND_INFO[$i]}" "$C_RESET" >"$TTY"
    done
    printf '\033[K\n' >"$TTY"
    printf '\033[K%sj/k or ↑/↓ move · space toggle · a all · n none · enter uninstall · q quit%s\n' \
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

to_remove=()
for ((i = 0; i < N; i++)); do
  if [ "${SELECTED[$i]}" -eq 1 ]; then
    to_remove+=("${FOUND_TOOLS[$i]}")
  fi
done

if [ "${#to_remove[@]}" -eq 0 ]; then
  echo "Nothing selected."
  exit 0
fi

for tool in "${to_remove[@]}"; do
  rm -f "$BIN_DIR/$tool" "$STATE_DIR/$tool.sha"
  echo "Removed $BIN_DIR/$tool"
done

# Clean up PATH entry and state dir only if no handy-tools remain installed.
remaining=0
for tool in "${ALL_TOOLS[@]}"; do
  if [ -e "$BIN_DIR/$tool" ]; then
    remaining=1
  fi
done

if [ "$remaining" -eq 0 ]; then
  rmdir "$STATE_DIR" 2>/dev/null || true
  for rc in "$HOME/.zshrc" "$HOME/.bashrc"; do
    if [ -f "$rc" ] && grep -qF '# handy-tools' "$rc"; then
      tmp=$(mktemp)
      grep -vF '# handy-tools' "$rc" > "$tmp" || true
      mv "$tmp" "$rc"
      echo "Removed handy-tools PATH entry from $rc"
    fi
  done
fi

echo "Done."
