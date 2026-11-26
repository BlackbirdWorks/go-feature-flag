package helper_test

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

// flagSpec is a flag of outputFlags(), as the reference model knows it.
type flagSpec struct {
	long    string
	short   string
	isValue bool
}

func specs() (map[string]flagSpec, map[string]flagSpec) {
	list := []flagSpec{
		{long: "output", short: "o", isValue: true},
		{long: "config", short: "c", isValue: true},
		{long: "endpoint", isValue: true},
		{long: "port", short: "p", isValue: true},
		{long: "dry-run", short: "n"},
		{long: "verbose", short: "v"},
	}
	longs, shorts := map[string]flagSpec{}, map[string]flagSpec{}

	for _, spec := range list {
		longs[spec.long] = spec
		if spec.short != "" {
			shorts[spec.short] = spec
		}
	}

	return longs, shorts
}

// modelState is the walk of the reference model: the last value of the output flag and the value slots.
type modelState struct {
	slots map[int]bool
	last  string
	found bool
}

// modelOutput is the reference: a walk from left to right with the rules of pflag written out again, with no pflag.
// It returns the last value of the output flag, whether there was one, and the indexes that are the value of a flag
// (an index past the end counts: the flag at the end that wants a value).
func modelOutput(args []string) (string, bool, map[int]bool) {
	state := &modelState{slots: map[int]bool{}}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--":
			return state.last, state.found, state.slots
		case strings.HasPrefix(arg, "--"):
			i = state.long(args, i)
		case arg[0] == '-' && len(arg) > 1:
			i = state.short(args, i)
		}
	}

	return state.last, state.found, state.slots
}

// value takes the value of the flag at i: the rest of the token when it has one, else the next argument. It returns
// the value, whether there was one, and the index of the last argument used.
func (m *modelState) value(args []string, i int, attached string, hasAttached bool) (string, bool, int) {
	if hasAttached {
		return attached, true, i
	}

	m.slots[i+1] = true

	if i+1 >= len(args) {
		return "", false, i
	}

	return args[i+1], true, i + 1
}

func (m *modelState) long(args []string, i int) int {
	longs, _ := specs()
	body := args[i][2:]

	if body[0] == '-' || body[0] == '=' {
		return i // malformed: skipped
	}

	name, attached, hasAttached := strings.Cut(body, "=")

	spec, known := longs[name]
	if !known {
		return i
	}

	if !spec.isValue {
		return i
	}

	value, ok, last := m.value(args, i, attached, hasAttached)
	if ok && name == "output" {
		m.last, m.found = value, true
	}

	return last
}

func (m *modelState) short(args []string, i int) int {
	_, shorts := specs()
	run := args[i][1:]

	if _, known := shorts[run[:1]]; !known {
		return i // the generator has no unknown shorthand inside a run
	}

	for run != "" {
		spec := shorts[run[:1]]
		if !spec.isValue {
			run = run[1:]

			continue
		}

		rest := strings.TrimPrefix(run[1:], "=")

		value, ok, last := m.value(args, i, rest, run[1:] != "")
		if ok && spec.short == "o" {
			m.last, m.found = value, true
		}

		return last
	}

	return i
}

// flagLikeValues are values that look like flags or terminators: as the value of a flag they are values.
func flagLikeValues() []string {
	return []string{jsonValue, textValue, shortJSON, flagOutputJSON, "-x", badToken, "--", "-", emptyName, "pos"}
}

// randomArgs builds a list of arguments from the forms of known flags of each kind, unknown flags, malformed tokens,
// terminators and positionals.
func randomArgs(rng *rand.Rand) []string {
	values := flagLikeValues()
	pick := func(list []string) string { return list[rng.IntN(len(list))] }

	forms := []func() []string{
		func() []string { return []string{flagOutput, pick(values)} },
		func() []string { return []string{"--output=" + pick([]string{jsonValue, textValue, "-x", "--"})} },
		func() []string { return []string{"-o", pick(values)} },
		func() []string { return []string{"-o" + pick([]string{jsonValue, textValue, "-x"})} },
		func() []string { return []string{"-o=" + pick([]string{jsonValue, textValue})} },
		func() []string { return []string{flagConfigLong, pick(values)} },
		func() []string { return []string{"-c", pick(values)} },
		func() []string { return []string{"--port", pick(append(values, "abc", "8080"))} },
		func() []string { return []string{pick([]string{"-n", "-v", "--dry-run", "--verbose", "-nv"})} },
		func() []string { return []string{combinedO, pick(values)} },
		func() []string { return []string{combinedO + pick([]string{jsonValue, textValue})} },
		func() []string { return []string{pick([]string{"--zzz", "--zzz=1", "-x"})} },
		func() []string { return []string{pick([]string{badToken, emptyName})} },
		func() []string { return []string{"--"} },
		func() []string { return []string{pick([]string{"pos", "-"})} },
		func() []string { return []string{flagEndpoint, pick(values)} },
	}

	var args []string

	for range rng.IntN(9) {
		args = append(args, forms[rng.IntN(len(forms))]()...)
	}

	// A flag at the end that wants its value and has none.
	if rng.IntN(6) == 0 {
		args = append(args, pick([]string{"-o", flagOutput, "-c", flagConfigLong, combinedO}))
	}

	return args
}

// Over random argument lists: the read terminates and does not panic, it equals the reference model, and a malformed
// or unknown token inserted where no value is expected changes nothing.
func TestRawOutputInProperties(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(20260101, 7))
	inserts := []string{badToken, emptyName, "--zzz", "--zzz=1", "-x"}

	for range 800 {
		args := randomArgs(rng)

		value, found := helper.RawOutputIn(outputFlags(), args)
		wantValue, wantFound, slots := modelOutput(args)

		require.Equal(t, wantFound, found, "args %q", args)
		require.Equal(t, wantValue, value, "args %q", args)

		for position := 0; position <= len(args); position++ {
			if slots[position] {
				continue
			}

			for _, token := range inserts {
				changed := append(append(append([]string{}, args[:position]...), token), args[position:]...)

				gotValue, gotFound := helper.RawOutputIn(outputFlags(), changed)

				require.Equal(t, value, gotValue, "inserting %q at %d of %q", token, position, args)
				require.Equal(t, found, gotFound, "inserting %q at %d of %q", token, position, args)
			}
		}
	}
}

// FuzzRawOutput: for any arguments the read terminates without panic, a malformed token put first changes nothing, and
// a value it returns is part of the arguments.
func FuzzRawOutput(f *testing.F) {
	for _, row := range rawOutputRows() {
		f.Add(strings.Join(row.args, "\n"))
	}

	f.Fuzz(func(t *testing.T, joined string) {
		args := strings.Split(joined, "\n")

		value, found := helper.RawOutputIn(outputFlags(), args)

		if found {
			assert.Contains(t, joined, value, "the value comes from the arguments")
		}

		prefixed, prefixedFound := helper.RawOutputIn(outputFlags(), append([]string{badToken}, args...))
		assert.Equal(t, value, prefixed, "a malformed token first changes nothing")
		assert.Equal(t, found, prefixedFound)
	})
}
