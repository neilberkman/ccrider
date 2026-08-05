#!/bin/sh
# POSIX sh on purpose: bash 5.3 (Homebrew's default on macOS, and present on
# GitHub macOS runners) deadlocks on heredocs larger than ~512 bytes.
#
# Assembles the MCPB bundle (https://github.com/anthropics/mcpb) from binaries
# GoReleaser has already built. Runs as a GoReleaser universal_binaries post
# hook, so the darwin universal binary and all per-platform binaries exist.
#
# The bundle carries one binary per OS (darwin universal, linux amd64,
# windows amd64); the manifest's platform_overrides select the right one.
# MCPB manifests can only discriminate by OS, not CPU arch, so linux/windows
# arm64 users should install from Homebrew/Scoop/releases instead.
set -eu

VERSION="$1"
DIST="${2:-dist}"

STAGE="$DIST/mcpb-stage"
rm -rf "$STAGE"
mkdir -p "$STAGE/bin"

cp "$DIST"/ccrider*_darwin_all/ccrider "$STAGE/bin/ccrider-darwin"
cp "$DIST"/ccrider_linux_amd64*/ccrider "$STAGE/bin/ccrider-linux"
cp "$DIST"/ccrider_windows_amd64*/ccrider.exe "$STAGE/bin/ccrider-win32.exe"
chmod +x "$STAGE/bin/"*

sed "s/__VERSION__/$VERSION/" > "$STAGE/manifest.json" <<'EOF'
{
  "manifest_version": "0.3",
  "name": "ccrider",
  "display_name": "CCRider",
  "version": "__VERSION__",
  "description": "Search, browse, and resume your coding agent sessions (Claude Code, Codex, Amp) with full-text search.",
  "author": {
    "name": "Neil Berkman",
    "email": "neil@xuku.com",
    "url": "https://github.com/neilberkman"
  },
  "homepage": "https://github.com/neilberkman/ccrider",
  "repository": {
    "type": "git",
    "url": "https://github.com/neilberkman/ccrider"
  },
  "license": "MIT",
  "keywords": ["claude-code", "codex", "sessions", "search", "history"],
  "server": {
    "type": "binary",
    "entry_point": "bin/ccrider-darwin",
    "mcp_config": {
      "command": "${__dirname}/bin/ccrider-darwin",
      "args": ["serve-mcp"],
      "platform_overrides": {
        "linux": {
          "command": "${__dirname}/bin/ccrider-linux"
        },
        "win32": {
          "command": "${__dirname}/bin/ccrider-win32.exe"
        }
      }
    }
  },
  "compatibility": {
    "platforms": ["darwin", "win32", "linux"]
  }
}
EOF

OUT="ccrider_${VERSION}.mcpb"
(cd "$STAGE" && zip -qr "../$OUT" .)
rm -rf "$STAGE"
echo "built $DIST/$OUT"
