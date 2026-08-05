#!/bin/sh
# POSIX sh on purpose: bash 5.3 (Homebrew's default on macOS, and present on
# GitHub macOS runners) deadlocks on heredocs larger than ~512 bytes.
#
# Generates the server.json submitted to the official MCP registry
# (registry.modelcontextprotocol.io) for a release. Called from the
# publish-mcp-registry job in .github/workflows/release.yml with the
# release version and the SHA-256 of the published .mcpb asset.
set -eu

VERSION="$1"
SHA256="$2"

cat <<EOF
{
  "\$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
  "name": "io.github.neilberkman/ccrider",
  "title": "CCRider",
  "description": "Full-text search, browse, and resume across your coding agent sessions (Claude Code, Codex, Amp)",
  "repository": {
    "url": "https://github.com/neilberkman/ccrider",
    "source": "github"
  },
  "websiteUrl": "https://github.com/neilberkman/ccrider",
  "version": "$VERSION",
  "packages": [
    {
      "registryType": "mcpb",
      "identifier": "https://github.com/neilberkman/ccrider/releases/download/v${VERSION}/ccrider_${VERSION}.mcpb",
      "fileSha256": "$SHA256",
      "transport": {
        "type": "stdio"
      }
    }
  ]
}
EOF
