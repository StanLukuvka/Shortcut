package cli

import "github.com/StanLukuvka/Shortcut/internal/cli/argparse"

// parsers is the parser of each command that has one, as its cmd_* builds it.
var parsers = map[string]*argparse.Parser{
	"new": argparse.New("new", helpText, argparse.Positional("name", 0), argparse.Value("desc", "--desc"), argparse.Value("script", "--script"),
		argparse.Value("cwd", "--cwd"), argparse.Flag("local", "--local"), argparse.Value("namespace", "--namespace"),
		argparse.Repeated("arg", "--arg"), argparse.Repeated("argDesc", "--arg-desc"), argparse.Flag("confirm", "--confirm")),
	"list": argparse.New("list", helpText, argparse.Optional("names", "--names")),
	"show": argparse.New("show", helpText, argparse.Positional("name", '+'), argparse.Flag("json", "--json"), argparse.Value("field", "--field"), argparse.Flag("skipLocal", "--global")),
	"rm": argparse.New("rm", helpText, argparse.Positional("name", '*'), argparse.Value("namespace", "--namespace"), argparse.Flag("yes", "-y", "--yes"),
		argparse.Flag("local", "--local"), argparse.Flag("skipLocal", "--global")),
	"update":     argparse.New("update", helpText, argparse.Positional("group", '?'), argparse.Flag("yes", "-y", "--yes")),
	"completion": argparse.New("completion", helpText, argparse.Choice("shell", "", "bash", "fish", "zsh")),
}
