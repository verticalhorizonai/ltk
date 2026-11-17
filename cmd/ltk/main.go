// Command ltk produces, checks and publishes a small corpus of Stellar XDR entries and reproduces two upstream bugs.
// This file is the CLI router: the command registry, the parsing of every command's flags and arguments, and the
// program's one exit.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/verticalhorizonai/ltk/internal/contract"
	"github.com/verticalhorizonai/ltk/internal/model"
)

// exitMapper is the interface through which main hands the command's error to the Exit code mapper
// (cmd/ltk/exit.go): it writes at most one ErrorLine to stderr and returns the exit code.
type exitMapper interface {
	exitCode(err error, stderr io.Writer) contract.ExitCode
}

// mapper is the Exit code mapper main uses.
var mapper exitMapper = errorLineMapper{}

// main is the one place that calls os.Exit, once.
func main() {
	ignoreSIGPIPE() // a reader that closes the pipe ends the run with a write error, not a signal
	ctx, stop := commandContext()
	err := dispatch(ctx, newRegistry(), os.Args[1:], stdoutWriter{w: os.Stdout})
	stop()
	os.Exit(int(mapper.exitCode(err, os.Stderr)))
}

// commandContext is the context every command runs under. A container-dependent command catches SIGINT and SIGTERM
// itself, in the Interrupt handler (cmd/ltk/interrupt.go), which cancels the work and tears down what it
// started; the mapper reports a cancelled run as interrupted (exit 1). The container-free commands keep the default
// behaviour of the signals.
func commandContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// ─── The registry ───────────────────────────────────────────────────────────────────────────────────────────────────

// contractCommands are the tool's commands, in the order `ltk --help` lists them. A command of this list that is not
// registered in the build is a usage error that says it is not available; a name outside this list is an unknown
// command.
var contractCommands = []string{
	"generate",
	"compare",
	"repro " + string(contract.ReproTargetWalletBackend729),
	"list",
	"check",
	"repro " + string(contract.ReproTargetJsSdk1766),
}

// reproTargets are the two reproduction names (contract.ReproTarget).
var reproTargets = []contract.ReproTarget{contract.ReproTargetWalletBackend729, contract.ReproTargetJsSdk1766}

// invocation is what a command's run receives: its positional arguments, in order, and standard output.
type invocation struct {
	args   []string
	stdout io.Writer
}

// command is one command of the registry.
type command struct {
	// name is the command's name without `ltk `: "list", "repro wallet-backend-729".
	name string
	// synopsis is the command's synopsis, line by line, exactly as `ltk --help` lists it.
	synopsis []string
	// positionals names the positional arguments, as the synopsis does (`<folder>`); exactly these many are required.
	positionals []string
	// setup declares the command's flags on fs and returns the function that runs it with their parsed values.
	setup func(fs *flag.FlagSet) func(ctx context.Context, inv invocation) error
}

// registry holds the registered commands, by name.
type registry struct {
	commands map[string]*command
}

// newRegistry builds the registry. Each command registers itself here: `ltk generate`, `ltk compare`,
// `ltk repro wallet-backend-729`, `ltk list`, `ltk check` and `ltk repro js-sdk-1766`.
func newRegistry() *registry {
	reg := emptyRegistry()
	reg.register(generateCommand())
	reg.register(compareCommand())
	reg.register(reproWalletBackendCommand())
	reg.register(listCommand())
	reg.register(checkCommand())
	reg.register(reproJsSdkCommand())
	return reg
}

// emptyRegistry is a registry with no command registered.
func emptyRegistry() *registry {
	return &registry{commands: map[string]*command{}}
}

// register adds c; a name outside contractCommands or registered twice is a defect of the program and panics at start.
func (r *registry) register(c *command) {
	known := false
	for _, n := range contractCommands {
		known = known || n == c.name
	}
	if !known {
		panic(fmt.Sprintf("ltk: command %q is not a command of the contract", c.name))
	}
	if _, dup := r.commands[c.name]; dup {
		panic(fmt.Sprintf("ltk: command %q registered twice", c.name))
	}
	r.commands[c.name] = c
}

// names returns the registered commands' names in the order of contractCommands.
func (r *registry) names() []string {
	var out []string
	for _, n := range contractCommands {
		if _, ok := r.commands[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// ─── Dispatch and parsing ───────────────────────────────────────────────────────────────────────────────────────────

// invocationLine is the form of an invocation, the first line of `ltk --help`.
const invocationLine = "ltk <command> [<subcommand>] [flags] [arguments]"

func isHelp(arg string) bool { return arg == "-h" || arg == "--help" || arg == "-help" || arg == "--h" }

// dispatch routes args to the registered command and runs it. Every problem with the command line is a usage error,
// returned before the command does any work.
func dispatch(ctx context.Context, reg *registry, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return model.Usage("no command given; run \"ltk --help\" for the commands")
	}
	if isHelp(args[0]) {
		return writeHelp(stdout, reg, reg.names())
	}
	name, rest := args[0], args[1:]
	if name == "repro" {
		if len(rest) == 0 {
			return model.Usage("ltk repro needs a target: %s or %s", reproTargets[0], reproTargets[1])
		}
		if isHelp(rest[0]) {
			var family []string
			for _, n := range reg.names() {
				if strings.HasPrefix(n, "repro ") {
					family = append(family, n)
				}
			}
			if len(family) == 0 {
				return model.Usage("the command \"ltk repro\" is not available in this build")
			}
			return writeHelp(stdout, reg, family)
		}
		known := false
		for _, t := range reproTargets {
			known = known || string(t) == rest[0]
		}
		if !known {
			return model.Usage("unknown reproduction target %q; the targets are %s and %s", rest[0], reproTargets[0], reproTargets[1])
		}
		name, rest = "repro "+rest[0], rest[1:]
	}
	c, ok := reg.commands[name]
	if !ok {
		for _, n := range contractCommands {
			if n == name {
				return model.Usage("the command \"ltk %s\" is not available in this build", name)
			}
		}
		return model.Usage("unknown command %q; run \"ltk --help\" for the commands", name)
	}
	return c.parseAndRun(ctx, rest, stdout)
}

// writeHelp prints the invocation line, then each named command's synopsis, word for word as the command declares
// it.
func writeHelp(stdout io.Writer, reg *registry, names []string) error {
	lines := []string{invocationLine}
	for _, n := range names {
		lines = append(lines, reg.commands[n].synopsis...)
	}
	_, err := io.WriteString(stdout, strings.Join(lines, "\n")+"\n")
	return err
}

// usageError is a usage error of this command: the message, then the command's synopsis.
func (c *command) usageError(format string, args ...any) error {
	return model.Usage("%s; usage: %s", fmt.Sprintf(format, args...), strings.Join(c.synopsis, " | "))
}

// parseAndRun parses the command's flags and positional arguments and runs it:
//   - flags are accepted before and after the positional arguments;
//   - `--name value` and `--name=value`, and Go's single-dash form, are the same flag;
//   - everything after `--` is positional;
//   - `-h` and `--help` print the command's synopsis on standard output (exit 0);
//   - an unknown flag, a missing or an extra positional argument is a usage error.
func (c *command) parseAndRun(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("ltk "+c.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the mapper writes the one ErrorLine; the flag package writes nothing
	run := c.setup(fs)
	flagArgs, positionals := splitArgs(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, werr := io.WriteString(stdout, strings.Join(c.synopsis, "\n")+"\n")
			return werr
		}
		return c.usageError("%v", err)
	}
	positionals = append(fs.Args(), positionals...) // fs.Args is empty unless a flag value broke the scan
	switch {
	case len(positionals) < len(c.positionals):
		return c.usageError("missing argument %s", c.positionals[len(positionals)])
	case len(positionals) > len(c.positionals):
		return c.usageError("unexpected argument %q", positionals[len(c.positionals)])
	}
	return run(ctx, invocation{args: positionals, stdout: stdout})
}

// splitArgs separates the flags (with the values of the flags that take one) from the positional arguments, so the
// flag package parses every flag wherever it stands. A flag fs does not declare is kept as a flag, for fs.Parse to
// report.
func splitArgs(fs *flag.FlagSet, args []string) (flags, positionals []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return flags, append(positionals, args[i+1:]...)
		}
		if len(a) < 2 || a[0] != '-' {
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return flags, positionals
}

// isBoolFlag reports whether f takes no value, as the flag package decides it.
func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
