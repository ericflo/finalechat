// Package finalechat exposes the repository's agent-facing documents and
// tooling as an embedded filesystem so the server can serve the exact files
// that live in Git.
package finalechat

import "embed"

// Assets holds AGENTS.md, the API reference, the OpenAPI document, the
// command-line helper, its installer, the Claude Code skill, and the terms
// and privacy documents the app shows.
//
//go:embed AGENTS.md docs/API.md docs/openapi.json docs/integrations.md docs/TERMS.md docs/PRIVACY.md cli/finalechat cli/install.sh skill/finalechat/SKILL.md sdk/finale-artifact.js
var Assets embed.FS
