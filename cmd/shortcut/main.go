package main

import (
	"os"

	"github.com/StanLukuvka/Shortcut/internal/commands"
)

func main() {
	os.Exit(commands.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
