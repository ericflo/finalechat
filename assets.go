// Package finalechat exposes the repository's agent-facing documents and
// tooling as an embedded filesystem so the server can serve the exact files
// that live in Git.
package finalechat

import "embed"

// Assets holds AGENTS.md, the API reference, the OpenAPI document, the
// command-line helper, its installer, and the Claude Code skill.
//
//go:embed AGENTS.md docs/API.md docs/openapi.json cli/finalechat cli/install.sh skill/finalechat/SKILL.md
var Assets embed.FS
