package shortcut

import "embed"

// Bundled holds the templates tree. The all: prefix keeps dot-prefixed
// entries such as templates/.shortcuts in the embedded filesystem.
//
//go:embed all:templates
var Bundled embed.FS
