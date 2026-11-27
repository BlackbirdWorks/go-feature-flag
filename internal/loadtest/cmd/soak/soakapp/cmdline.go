package soakapp

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alecthomas/kong"
)

const (
	// helpWidth is the upper bound on help text wrapping width so output does not depend on terminal width.
	helpWidth = 80

	flagHelp   = "help"
	flagTarget = "target"
)

// errUsage is wrapped by the error of command line flags that do not parse.
var errUsage = errors.New("invalid command line")

type cli struct {
	Target        string        `default:"library"               help:"library | relay"          name:"target"`
	Endpoint      string        `default:"semaphore-eval"        help:"${helpEndpoint}"          name:"endpoint"`
	Protocol      string        `default:"http1"                 help:"${helpProtocol}"          name:"protocol"`
	Exporter      string        `default:"discard"               help:"discard | slow | failing" name:"exporter"`
	FlagKey       string        `default:"rule-bool-track"       help:"${helpFlag}"              name:"flag"`
	CPUProfile    string        `help:"${helpCPUProfile}"        name:"cpuprofile"`
	Duration      time.Duration `default:"60s"                   help:"total run time"           name:"duration"`
	Interval      time.Duration `default:"5s"                    help:"sampling interval"        name:"interval"`
	FlushMs       int64         `default:"100"                   help:"${helpFlushMs}"           name:"flush-ms"`
	MaxEvents     int64         `default:"10000"                 help:"${helpMaxEvents}"         name:"max-events"`
	HeapTolerance float64       `default:"0.25"                  help:"${helpHeapTolerance}"     name:"heap-tolerance"`
	Workers       int           `help:"${helpWorkers}"           name:"workers"`
	Warmup        int           `default:"0"                     help:"${helpWarmup}"            name:"warmup"`
	Help          bool          `help:"Print this help and exit" name:"help"                     short:"h"`
}

func helpVars() kong.Vars {
	return kong.Vars{
		"helpEndpoint":   "relay endpoint: semaphore-eval | semaphore-allflags | ofrep-eval | ofrep-bulk",
		"helpProtocol":   "relay client and server protocol: http1 (keep-alive) | h2c (HTTP/2 cleartext)",
		"helpFlag":       "flag key to evaluate (tracking on by default)",
		"helpCPUProfile": "write a CPU profile of the whole run to this file",
		"helpFlushMs":    "exporter flush interval (ms)",
		"helpMaxEvents": "exporter MaxEventInMemory " +
			"(a small backlog keeps the heap measuring leaks, not the pending events)",
		"helpHeapTolerance": "relative HeapInuse growth tolerated",
		"helpWorkers":       "number of load goroutines (default GOMAXPROCS)",
		"helpWarmup":        "warm-up samples ignored by the verdict (default max(2, 20%))",
	}
}

func isValueFlag(name string) bool {
	switch name {
	case flagTarget, "endpoint", "protocol", "exporter", "flag", "cpuprofile",
		"duration", "interval", "flush-ms", "max-events", "heap-tolerance", "workers", "warmup":
		return true
	default:
		return false
	}
}

// rewriteFlagArg normalizes one flag argument from legacy single-dash Go flag form to kong's double-dash form
// when it names a known flag, returning the rewritten token and whether the next argument is its separated value.
func rewriteFlagArg(arg string) (string, bool) {
	if strings.HasPrefix(arg, "--") {
		name, _, hasEq := strings.Cut(arg[2:], "=")

		return arg, !hasEq && isValueFlag(name)
	}

	if strings.HasPrefix(arg, "-") && len(arg) > 1 {
		name, _, hasEq := strings.Cut(arg[1:], "=")
		if isValueFlag(name) {
			return "-" + arg, !hasEq
		}

		if name == flagHelp {
			return "-" + arg, false
		}
	}

	return arg, false
}

// normalizeArgs converts legacy single-dash Go flag spellings (such as -target or -help) to kong's
// double-dash form while leaving -h, unknown single-dash flags, flag values, and args after "--" untouched.
// Separated values that start with "-" (such as "-cpuprofile -cpu.pprof" or "-workers -1") are joined with
// "=" so kong parses them as values rather than flags, matching standard library flag behaviour.
func normalizeArgs(args []string) []string {
	out := make([]string, 0, len(args))

	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]
		if arg == "--" {
			out = append(out, args[idx:]...)

			break
		}

		rewritten, consumesNext := rewriteFlagArg(arg)
		if consumesNext && idx+1 < len(args) {
			idx++
			next := args[idx]

			if strings.HasPrefix(next, "-") {
				out = append(out, rewritten+"="+next)
			} else {
				out = append(out, rewritten, next)
			}

			continue
		}

		out = append(out, rewritten)
	}

	return out
}

func newParser(command *cli, out io.Writer) (*kong.Kong, error) {
	parser, err := kong.New(
		command,
		kong.Name("soak"),
		kong.Description("Run a max-rate soak load against the library or a real relay proxy."),
		kong.NoDefaultHelp(),
		kong.Writers(out, out),
		kong.Exit(func(int) {}),
		kong.ConfigureHelp(kong.HelpOptions{WrapUpperBound: helpWidth}),
		helpVars(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUsage, err)
	}

	return parser, nil
}

func parseCommandLine(args []string) (cli, error) {
	var command cli

	parser, err := newParser(&command, io.Discard)
	if err != nil {
		return cli{}, err
	}

	_, err = parser.Parse(normalizeArgs(args))
	if err != nil {
		return cli{}, fmt.Errorf("%w: %w", errUsage, err)
	}

	return command, nil
}

func usageText() string {
	var (
		command cli
		out     strings.Builder
	)

	parser, err := newParser(&command, &out)
	if err != nil {
		return err.Error()
	}

	ctx, err := kong.Trace(parser, nil)
	if err != nil {
		return err.Error()
	}

	_ = ctx.PrintUsage(false)

	return out.String()
}
