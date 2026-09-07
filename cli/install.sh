#!/bin/sh
# Finalechat command-line installer.
#
#   curl -fsSL https://www.finalechat.com/install.sh | sh
#
# Downloads the single-file `finalechat` client (Python 3.9+, no dependencies)
# into ~/.local/bin (or $FINALECHAT_INSTALL_DIR). If FINALECHAT_TOKEN is set,
# it signs you in straight away.
#
# Environment:
#   FINALECHAT_INSTALL_DIR  where to put the executable (default ~/.local/bin)
#   FINALECHAT_URL          server origin (default https://www.finalechat.com)
#   FINALECHAT_TOKEN        API token from Settings → Agents; logs in when set

set -eu

base_url="${FINALECHAT_URL:-https://www.finalechat.com}"
base_url="${base_url%/}"
install_dir="${FINALECHAT_INSTALL_DIR:-$HOME/.local/bin}"
target="$install_dir/finalechat"
source_url="$base_url/cli/finalechat"

say() { printf '%s\n' "$*"; }
fail() { printf 'finalechat installer: %s\n' "$*" >&2; exit 1; }

# --- python -----------------------------------------------------------------
python_bin=""
for candidate in python3 python; do
    if command -v "$candidate" >/dev/null 2>&1; then
        if "$candidate" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 9) else 1)' 2>/dev/null; then
            python_bin="$candidate"
            break
        fi
    fi
done
[ -n "$python_bin" ] || fail "Python 3.9 or newer is required (python3 not found or too old)."

# --- download ---------------------------------------------------------------
mkdir -p "$install_dir" || fail "cannot create $install_dir"
tmp="$(mktemp "$install_dir/.finalechat.XXXXXX")" || fail "cannot create a temporary file in $install_dir"
trap 'rm -f "$tmp"' EXIT INT TERM

if command -v curl >/dev/null 2>&1; then
    curl -fsSL --proto '=https,http' -o "$tmp" "$source_url" || fail "download failed: $source_url"
elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$tmp" "$source_url" || fail "download failed: $source_url"
else
    fail "curl or wget is required."
fi

head -c 200 "$tmp" | grep -q 'python3' || fail "downloaded file does not look like the finalechat client"
"$python_bin" -m py_compile "$tmp" >/dev/null 2>&1 || fail "downloaded client failed to compile with $python_bin"

chmod 0755 "$tmp"
mv -f "$tmp" "$target"
trap - EXIT INT TERM

version="$("$python_bin" "$target" --version 2>/dev/null || echo 'finalechat')"
say "Installed $version to $target"

# --- PATH hint --------------------------------------------------------------
case ":$PATH:" in
    *":$install_dir:"*) on_path=1 ;;
    *) on_path=0 ;;
esac
if [ "$on_path" -eq 0 ]; then
    say ""
    say "  $install_dir is not on your PATH. Add this to your shell profile:"
    say "    export PATH=\"$install_dir:\$PATH\""
fi

# --- sign in ----------------------------------------------------------------
say ""
if [ -n "${FINALECHAT_TOKEN:-}" ]; then
    if FINALECHAT_URL="$base_url" "$python_bin" "$target" login "$FINALECHAT_TOKEN"; then
        say ""
        say "Next:"
        say "  finalechat say \"Hello from my terminal\"     send yourself a message"
        say "  finalechat install claude-code              mirror Claude Code sessions to your phone"
    else
        say "Sign-in failed; run \`finalechat login\` with a token from $base_url/settings/agents"
    fi
else
    say "Next:"
    say "  finalechat login                            paste a token from $base_url/settings/agents"
    say "  finalechat say \"Hello from my terminal\"     send yourself a message"
    say "  finalechat install claude-code              mirror Claude Code sessions to your phone"
fi
say ""
say "Docs: $base_url/AGENTS.md"
