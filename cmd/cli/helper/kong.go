package helper

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/alecthomas/kong"
)

// HelpWidth is the upper bound on help text wrapping width so output does not depend on terminal width.
const HelpWidth = 80

// ErrEarlyExit is returned from a BeforeReset flag hook (such as [HelpFlag]) to stop parsing with exit code 0 before
// validation runs.
var (
	ErrEarlyExit = errors.New("early exit")
	errNilParser = errors.New("nil kong parser")
)

// HelpFlag is a kong --help / -h flag that honours --help=false and exits parsing via [ErrEarlyExit] without panicking.
type HelpFlag bool

// RegisterHelpFlag sets kong's Model.HelpFlag to the explicit "help" flag after building the parser so kong's default
// help footer ("Run \"<app> <command> --help\" for more information on a command.") is still printed even when
// [kong.NoDefaultHelp] is enabled. Pass it to [kong.PostBuild].
func RegisterHelpFlag(app *kong.Kong) error {
	if app == nil || app.Model == nil {
		return errNilParser
	}

	for _, flag := range app.Model.Flags {
		if flag != nil && flag.Name == "help" {
			app.Model.HelpFlag = flag

			break
		}
	}

	return nil
}

// BeforeReset prints kong help when the flag decoded to true and returns [ErrEarlyExit].
func (HelpFlag) BeforeReset(ctx *kong.Context) error {
	for _, item := range ctx.Path {
		if item.Flag == nil || item.Flag.Name != "help" {
			continue
		}

		val, ok := ctx.FlagValue(item.Flag).(HelpFlag)
		if ok && bool(val) {
			err := ctx.PrintUsage(false)
			if err != nil {
				return fmt.Errorf("print help: %w", err)
			}

			return ErrEarlyExit
		}
	}

	return nil
}

// DecodeString decodes kong string values, stripping one leading "=" from short-flag tails such as "-c=cfg.yaml"
// or "-o=json" while preserving literal leading "=" in separated or long-flag values.
func DecodeString(ctx *kong.DecodeContext, target reflect.Value) error {
	token, err := ctx.Scan.PopValue("string")
	if err != nil {
		return err //nolint:wrapcheck // Kong caller wraps with flag name and handleParseError adds ErrUsage
	}

	val := token.String()
	if token.Type == kong.ShortFlagTailToken {
		val = strings.TrimPrefix(val, "=")
	}

	target.SetString(val)

	return nil
}

// Parse runs parser.Parse(args) and translates [ErrEarlyExit] into (nil, true, nil).
func Parse(parser *kong.Kong, args []string) (*kong.Context, bool, error) {
	kctx, err := parser.Parse(args)
	if errors.Is(err, ErrEarlyExit) {
		return nil, true, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("%w", err)
	}

	return kctx, false, nil
}
