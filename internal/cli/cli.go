// Package cli is the printpipe command: every subcommand, its output and its
// exit code, over an injected System.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Version is set at build time:
// -ldflags "-X github.com/alexgred/printpipe/internal/cli.Version=...".
var Version = "dev"

// env is what a command runs with.
type env struct {
	ctx            context.Context
	stdout, stderr io.Writer
	sys            System
	flags          map[string]string
}

// command is one subcommand. Its flags all take a value.
type command struct {
	name  string
	usage string // the line in help; empty for the hidden server command
	flags []string
	run   func(env) error
}

func commands() []command {
	return []command{
		{name: "help", usage: "help                      Show this list.", run: help},
		{name: "start", usage: "start                     Start the agent in the background."},
		{name: "stop", usage: "stop                      Stop the agent."},
		{name: "restart", usage: "restart                   Restart the agent, or start it when it is stopped."},
		{name: "startup", usage: "startup [on|off]          Show, or turn on or off, starting the agent at login."},
		{name: "config", usage: "config [flags]            Show the configuration, or change it with flags."},
		{name: "version", usage: "version                   Print the version.", run: version},
		{name: "install", usage: "install                   Install the agent for this user, find the printer and start."},
		{name: "upgrade", usage: "upgrade                   Install the latest release."},
		{name: "remove", usage: "remove [--queue]          Stop and remove the agent; --queue removes its printer queue too."},
		{name: "status", usage: "status                    Show whether the agent runs, its version, printer and files."},
		{name: "test-print", usage: "test-print [--size W,H]   Print the built-in test label."},
		{name: "serve", flags: []string{"config", "dry-run"}, run: serve},
	}
}

// Run runs the command line args (without the program name) and returns the
// exit code: 0 on success, 1 on any failure, 2 on wrong usage.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, sys System) int {
	e := env{ctx: ctx, stdout: stdout, stderr: stderr, sys: sys}
	err := dispatch(e, args)
	var usage usageError
	var fail *failure
	switch {
	case err == nil:
		return 0
	case errors.As(err, &usage):
		fmt.Fprintf(stderr, "error: %s\n  Run printpipe help to see the commands.\n", usage)
		return 2
	case errors.As(err, &fail):
		fmt.Fprintf(stderr, "error: %s\n", fail.msg)
		for _, h := range fail.hints {
			fmt.Fprintf(stderr, "  %s\n", h)
		}
		return 1
	default:
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
}

func dispatch(e env, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		args = []string{"help"}
	}
	for _, c := range commands() {
		if c.name != args[0] {
			continue
		}
		if c.run == nil {
			return &failure{msg: fmt.Sprintf("printpipe %s is not implemented yet", c.name)}
		}
		flags, err := parseFlags(c, args[1:])
		if err != nil {
			return err
		}
		e.flags = flags
		return c.run(e)
	}
	return usageError(fmt.Sprintf("unknown command %q", args[0]))
}

// parseFlags reads --name value and --name=value. No command takes positional
// arguments yet.
func parseFlags(c command, args []string) (map[string]string, error) {
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, ok := strings.CutPrefix(a, "--")
		if !ok || name == "" {
			return nil, usageError(fmt.Sprintf("unexpected argument %q", a))
		}
		name, value, hasValue := strings.Cut(name, "=")
		if !slices.Contains(c.flags, name) {
			return nil, usageError(fmt.Sprintf("unknown flag --%s", name))
		}
		if !hasValue {
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
				return nil, usageError(fmt.Sprintf("flag --%s needs a value", name))
			}
			i++
			value = args[i]
		}
		flags[name] = value
	}
	return flags, nil
}

// usageError is wrong usage: exit 2.
type usageError string

func (e usageError) Error() string { return string(e) }

// failure is a command that failed: exit 1, with what to do on the lines
// under the error.
type failure struct {
	msg   string
	hints []string
}

func (f *failure) Error() string { return f.msg }

func help(e env) error {
	var b strings.Builder
	b.WriteString("printpipe prints raw label jobs that web pages send it on this computer.\n\nUsage: printpipe <command> [flags]\n\nCommands:\n")
	for _, c := range commands() {
		if c.usage != "" {
			b.WriteString("  " + c.usage + "\n")
		}
	}
	_, err := io.WriteString(e.stdout, b.String())
	return err
}

func version(e env) error {
	_, err := fmt.Fprintf(e.stdout, "printpipe %s\n", Version)
	return err
}
