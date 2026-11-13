// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-6). Don't hand-edit without checking the plan.

package cli

import _ "embed"

//go:embed completion/bash.txt
var bashCompletion string

//go:embed completion/zsh.txt
var zshCompletion string

//go:embed completion/fish.txt
var fishCompletion string

var completionScripts = map[string]string{
	"bash": bashCompletion,
	"zsh":  zshCompletion,
	"fish": fishCompletion,
}
