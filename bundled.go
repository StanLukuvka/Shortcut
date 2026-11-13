// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-6). Don't hand-edit without checking the plan.

package shortcut

import "embed"

// Bundled holds the templates tree. The all: prefix keeps dot-prefixed
// entries such as templates/.shortcuts in the embedded filesystem.
//
//go:embed all:templates
var Bundled embed.FS
