package cli

import "github.com/StanLukuvka/Shortcut/internal/cli/argparse"

// parsers is the parser of each command that has one, as its cmd_* builds it (the arguments in the order the Python version adds them).
var parsers = map[string]*argparse.Parser{
	"new": argparse.New("new", helpText, argparse.Positional("name", 0), argparse.Value("desc", "--desc"), argparse.Value("script", "--script"), argparse.Value("copy", "--copy"),
		argparse.Value("cwd", "--cwd"), argparse.Flag("local", "--local"), argparse.Value("namespace", "--namespace"), argparse.Value("by", "--by"),
		argparse.Repeated("arg", "--arg"), argparse.Repeated("arg_desc", "--arg-desc"), argparse.Flag("confirm", "--confirm")).Exclusive("script", "copy"),
	"list":     argparse.New("list", helpText, argparse.Optional("names", "--names")),
	"show":     argparse.New("show", helpText, argparse.Positional("name", '+'), argparse.Flag("json", "--json"), argparse.Flag("skip_local", "--global")),
	"location": argparse.New("location", helpText, argparse.Positional("name", '+'), argparse.Flag("skip_local", "--global")),
	"rm": argparse.New("rm", helpText, argparse.Positional("name", '*'), argparse.Value("namespace", "--namespace"), argparse.Flag("yes", "-y", "--yes"),
		argparse.Flag("local", "--local"), argparse.Flag("skip_local", "--global")),
	"promote": argparse.New("promote", helpText, argparse.Positional("name", 0), argparse.Value("new_name", "--as"), argparse.Flag("yes", "-y", "--yes"), argparse.Flag("force", "--force")),
	"update":  argparse.New("update", helpText, argparse.Positional("group", '?'), argparse.Flag("yes", "-y", "--yes")),
	"init": argparse.New("init", helpText, argparse.Positional("source", '?'), argparse.Flag("is_global", "--global"), argparse.Flag("bundled", "--bundled"), argparse.Flag("yes", "-y", "--yes"),
		argparse.Flag("replace", "--replace")),
	"completion": argparse.New("completion", helpText, argparse.Choice("shell", "bash", "bash", "fish", "zsh")),
}
