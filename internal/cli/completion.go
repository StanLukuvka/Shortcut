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
