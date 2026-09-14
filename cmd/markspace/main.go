// Command markspace runs the Markspace note server and manages a running
// server from the shell. docs/cli.md describes every command.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"strings"
)

// buildVersion is set at link time with
// -ldflags "-X main.buildVersion=v1.2.3". Empty means the build info decides.
var buildVersion string

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// Exit codes of the command.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// cli holds the standard streams of one invocation.
type cli struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// usageError is a wrong invocation of cmd, such as an unknown flag or a
// missing required flag.
type usageError struct {
	cmd string
	msg string
}

func (e usageError) Error() string { return e.msg }

// run executes one invocation and returns its exit code.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := &cli{stdin: stdin, stdout: stdout, stderr: stderr}
	err := c.dispatch(args)
	var ue usageError
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.As(err, &ue):
		help := "markspace help"
		if ue.cmd != "" {
			help += " " + ue.cmd
		}
		fmt.Fprintf(stderr, "markspace: %s. Run %q for usage.\n", ue.msg, help)
		return exitUsage
	}
	fmt.Fprintln(stderr, "markspace: "+err.Error())
	return exitFailure
}

func isHelpFlag(s string) bool { return s == "-h" || s == "-help" || s == "--help" }

// dispatch runs the command named by the first argument. No arguments, or a
// first argument that is a flag, runs the server as older versions did.
func (c *cli) dispatch(args []string) error {
	if len(args) == 0 || (strings.HasPrefix(args[0], "-") && !isHelpFlag(args[0])) {
		return c.legacy(args)
	}
	name, rest := args[0], args[1:]
	switch {
	case name == "help" && len(rest) > 0:
		return c.dispatch(append(slices.Clone(rest), "-h"))
	case name == "help" || isHelpFlag(name):
		c.printUsage("", nil)
		return flag.ErrHelp
	}
	commands := map[string]func([]string) error{
		"serve":   c.serve,
		"version": c.version,
		"config":  c.configCmd,
		"health":  c.health,
		"status":  c.status,
		"user":    c.user,
		"reindex": c.reindex,
		"backup":  c.backup,
	}
	fn, ok := commands[name]
	if !ok {
		return usageError{msg: fmt.Sprintf("unknown command %q", name)}
	}
	return fn(rest)
}

// flags returns a flag set that reports errors to the caller only.
func (c *cli) flags(cmd string) *flag.FlagSet {
	fs := flag.NewFlagSet("markspace "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parse parses the flags of cmd. -h prints the usage and returns
// flag.ErrHelp. A positional argument is a usage error.
func (c *cli) parse(fs *flag.FlagSet, cmd string, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			c.printUsage(cmd, fs)
			return flag.ErrHelp
		}
		return usageError{cmd: cmd, msg: err.Error()}
	}
	if fs.NArg() > 0 {
		return usageError{cmd: cmd, msg: fmt.Sprintf("unexpected argument %q", fs.Arg(0))}
	}
	return nil
}

// group runs a subcommand of a command with subcommands, such as user.
func (c *cli) group(name string, args []string, subs map[string]func([]string) error) error {
	if len(args) == 0 {
		return usageError{cmd: name, msg: "missing subcommand"}
	}
	if isHelpFlag(args[0]) {
		c.printUsage(name, nil)
		return flag.ErrHelp
	}
	fn, ok := subs[args[0]]
	if !ok {
		return usageError{cmd: name, msg: fmt.Sprintf("unknown subcommand %q", args[0])}
	}
	return fn(args[1:])
}

// printUsage writes the usage of cmd and the defaults of its flags to
// standard output.
func (c *cli) printUsage(cmd string, fs *flag.FlagSet) {
	fmt.Fprint(c.stdout, usages[cmd])
	if fs == nil {
		return
	}
	hasFlags := false
	fs.VisitAll(func(*flag.Flag) { hasFlags = true })
	if hasFlags {
		fmt.Fprintln(c.stdout, "\nFlags:")
		fs.SetOutput(c.stdout)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
	}
}

// usages holds the usage text of each command, keyed by the words after
// "markspace".
var usages = map[string]string{
	"": `Usage: markspace <command> [flags]

Server:
  serve            run the server in the foreground
  version          print the version
  config example   print an example configuration file
  config check     load the configuration and print the effective values

Management of a running server:
  health           check that the server answers
  status           print version, uptime, counts, jobs and plugins
  user list        list accounts
  user add         create an account
  user passwd      reset a password and sign out its sessions
  user admin       grant or revoke admin rights
  user disable     deactivate an account and sign out its sessions
  user enable      reactivate an account
  reindex          reread every markdown file into the index
  backup           download a backup archive of the data directory

Run "markspace help <command>" for the flags of a command. docs/cli.md
describes the token that the management commands use.
`,
	"serve": `Usage: markspace serve [-config path] [-listen addr] [-data dir]

Runs the server in the foreground until SIGINT or SIGTERM. At start it writes
the Markspace CLI token to <dataDir>/cli.token when the file is missing or its
token was revoked.
`,
	"version": `Usage: markspace version

Prints the version of this binary.
`,
	"config": `Usage: markspace config <example|check> [flags]

  config example   print an example configuration file
  config check     load the configuration and print the effective values
`,
	"config example": `Usage: markspace config example

Prints a commented example configuration file.
`,
	"config check": `Usage: markspace config check [-config path] [-listen addr] [-data dir]

Loads the configuration as serve does and prints the effective values with
API keys and headers redacted. It exits 1 when the file is invalid.
`,
	"health": `Usage: markspace health [-config path] [-data dir] [-url url]

Calls GET /healthz and exits 0 when the server answers within 2 seconds. It
needs no token.
`,
	"status": `Usage: markspace status [-json] [connection flags]

Prints the version, uptime, listen address, data directory, account and space
counts, job counts by status and the health of each plugin.
`,
	"user": `Usage: markspace user <subcommand> [flags]

  user list      list accounts
  user add       create an account
  user passwd    reset a password and sign out its sessions
  user admin     grant or revoke admin rights
  user disable   deactivate an account and sign out its sessions
  user enable    reactivate an account
`,
	"user list": `Usage: markspace user list [-json] [connection flags]

Lists every account.
`,
	"user add": `Usage: markspace user add -email address [-name name] [-admin] [-password-stdin] [connection flags]

Creates an account. Without -password-stdin it generates a password and
prints it once.
`,
	"user passwd": `Usage: markspace user passwd -email address [-password-stdin] [connection flags]

Sets a new password and signs out every session of the account. Without
-password-stdin it generates a password and prints it once.
`,
	"user admin": `Usage: markspace user admin -email address [-revoke] [connection flags]

Grants admin rights, or revokes them with -revoke. The last active admin
keeps its rights.
`,
	"user disable": `Usage: markspace user disable -email address [connection flags]

Deactivates an account and signs out its sessions. The last active admin
cannot be deactivated.
`,
	"user enable": `Usage: markspace user enable -email address [connection flags]

Reactivates an account.
`,
	"reindex": `Usage: markspace reindex [connection flags]

Rereads every markdown file of every space into the index.
`,
	"backup": `Usage: markspace backup [-out file] [connection flags]

Writes a gzip tar of the data directory with a consistent database snapshot
to standard output, or to -out.
`,
}

// version returns buildVersion when the linker set it, and otherwise the VCS
// revision and Go version from the build info.
func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	rev, dirty := "", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 12 {
				rev = s.Value[:12]
			} else {
				rev = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return info.GoVersion
	}
	return rev + dirty + " " + info.GoVersion
}

// version serves "markspace version".
func (c *cli) version(args []string) error {
	fs := c.flags("version")
	if err := c.parse(fs, "version", args); err != nil {
		return err
	}
	fmt.Fprintln(c.stdout, "markspace "+version())
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
