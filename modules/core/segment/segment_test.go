package segment_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/rules/parser"
)

const (
	segBeta  = "beta"
	errName  = "invalid name"
	segEmpty = "empty"
)

func TestDefinitionValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		def     segment.Definition
		wantErr string
	}{
		{name: "file", def: segment.Definition{Name: "a", Type: segment.TypeFile}},
		{
			name: "valkey with a policy",
			def:  segment.Definition{Name: "a-b_1", Type: segment.TypeValkey, OnError: segment.OnErrorMember},
		},
		{
			name: "redis with a policy",
			def:  segment.Definition{Name: "a-b_1", Type: segment.TypeRedis, OnError: segment.OnErrorMember},
		},
		{
			name: "each policy: notMember",
			def:  segment.Definition{Name: "a", Type: segment.TypeFile, OnError: segment.OnErrorNotMember},
		},
		{
			name: "each policy: unknown",
			def:  segment.Definition{Name: "a", Type: segment.TypeFile, OnError: segment.OnErrorUnknown},
		},
		{name: "bad name", def: segment.Definition{Name: "A", Type: segment.TypeFile}, wantErr: errName},
		{name: "empty name", def: segment.Definition{Type: segment.TypeFile}, wantErr: errName},
		{
			name:    "long name",
			def:     segment.Definition{Name: strings.Repeat("a", 65), Type: segment.TypeFile},
			wantErr: errName,
		},
		{
			name:    "bad type",
			def:     segment.Definition{Name: "a", Type: "s3"},
			wantErr: `invalid type: want file, redis or valkey, got "s3"`,
		},
		{name: "empty type", def: segment.Definition{Name: "a"}, wantErr: "invalid type"},
		{
			name:    "bad policy",
			def:     segment.Definition{Name: "a", Type: segment.TypeFile, OnError: "match"},
			wantErr: "invalid onError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.def.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, segment.ErrInvalidDefinition)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDefinitionResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		onError segment.OnError
		state   segment.State
		want    segment.State
	}{
		{name: "default keeps unknown", onError: "", state: segment.Unknown, want: segment.Unknown},
		{name: "unknown keeps unknown", onError: segment.OnErrorUnknown, state: segment.Unknown, want: segment.Unknown},
		{name: "notMember", onError: segment.OnErrorNotMember, state: segment.Unknown, want: segment.NotMember},
		{name: "member", onError: segment.OnErrorMember, state: segment.Unknown, want: segment.Member},
		{
			name:    "member policy leaves an answer",
			onError: segment.OnErrorMember,
			state:   segment.NotMember,
			want:    segment.NotMember,
		},
		{
			name:    "notMember policy leaves an answer",
			onError: segment.OnErrorNotMember,
			state:   segment.Member,
			want:    segment.Member,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			def := segment.Definition{Name: "a", Type: segment.TypeFile, OnError: tt.onError}
			assert.Equal(t, tt.want, def.Resolve(tt.state))
		})
	}
}

func TestCatalog(t *testing.T) {
	t.Parallel()

	catalog, err := segment.NewCatalog(
		segment.Definition{Name: "b", Type: segment.TypeFile},
		segment.Definition{Name: "a", Type: segment.TypeValkey, OnError: segment.OnErrorMember},
	)
	require.NoError(t, err)

	assert.Equal(t, []string{"a", "b"}, catalog.Names())

	def, ok := catalog.Lookup("a")
	assert.True(t, ok)
	assert.Equal(t, segment.OnErrorMember, def.OnError)

	_, ok = catalog.Lookup("c")
	assert.False(t, ok)

	var none *segment.Catalog

	_, ok = none.Lookup("a")
	assert.False(t, ok)
	assert.Empty(t, none.Names())
}

func TestNewCatalogRejects(t *testing.T) {
	t.Parallel()

	_, err := segment.NewCatalog(
		segment.Definition{Name: "a", Type: segment.TypeFile},
		segment.Definition{Name: "a", Type: segment.TypeValkey},
	)
	require.ErrorIs(t, err, segment.ErrInvalidDefinition)
	assert.Contains(t, err.Error(), "duplicated")

	_, err = segment.NewCatalog(segment.Definition{Name: "A", Type: segment.TypeFile})
	require.ErrorIs(t, err, segment.ErrInvalidDefinition)
}

func TestStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		a, b      segment.Status
		wantWorse segment.Status
	}{
		{name: "none and healthy", a: segment.StatusNone, b: segment.StatusHealthy, wantWorse: segment.StatusHealthy},
		{
			name:      "healthy and not configured",
			a:         segment.StatusHealthy,
			b:         segment.StatusNotConfigured,
			wantWorse: segment.StatusNotConfigured,
		},
		{
			name:      "not configured and error",
			a:         segment.StatusNotConfigured,
			b:         segment.StatusStoreError,
			wantWorse: segment.StatusStoreError,
		},
		{
			name:      "error and healthy",
			a:         segment.StatusStoreError,
			b:         segment.StatusHealthy,
			wantWorse: segment.StatusStoreError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantWorse, tt.a.Worse(tt.b))
			assert.Equal(t, tt.wantWorse, tt.b.Worse(tt.a))
		})
	}

	assert.Empty(t, segment.StatusNone.String())
	assert.Equal(t, "HEALTHY", segment.StatusHealthy.String())
	assert.Equal(t, "NOT_CONFIGURED", segment.StatusNotConfigured.String())
	assert.Equal(t, "STORE_ERROR", segment.StatusStoreError.String())
	assert.Empty(t, segment.Status(99).String())
}

func TestMemoryProvider(t *testing.T) {
	t.Parallel()

	members := map[string][]string{"a": {"x", "y"}, segEmpty: nil}
	provider := segment.NewMemoryProvider(members)
	members["a"][0] = "changed" // the provider copied the members

	lookups := []segment.Lookup{
		{Segment: "a", ID: "x"},
		{Segment: "a", ID: "z"},
		{Segment: segEmpty, ID: "x"},
		{Segment: "ghost", ID: "x"},
	}

	require.NoError(t, provider.Contains(context.Background(), lookups))

	states := make([]segment.State, len(lookups))
	for i := range lookups {
		states[i] = lookups[i].State
	}

	assert.Equal(t, []segment.State{segment.Member, segment.NotMember, segment.NotMember, segment.Unknown}, states)
	assert.Equal(t, "a", lookups[0].Segment)
	assert.Equal(t, "x", lookups[0].ID)
}

func TestMemoryProviderCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lookups := []segment.Lookup{{Segment: "a", ID: "x"}}
	err := segment.NewMemoryProvider(map[string][]string{"a": {"x"}}).Contains(ctx, lookups)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, segment.Unknown, lookups[0].State)
}

//nolint:paralleltest // AllocsPerRun counts allocations from concurrent tests across the process
func TestMemoryProviderDoesNotAllocate(t *testing.T) {
	provider := segment.NewMemoryProvider(map[string][]string{"a": {"x"}})
	lookups := []segment.Lookup{{Segment: "a", ID: "x"}, {Segment: "a", ID: "y"}}
	ctx := context.Background()

	allocs := testing.AllocsPerRun(100, func() {
		_ = provider.Contains(ctx, lookups)
	})

	assert.InDelta(t, 0, allocs, 0)
}

type errReader struct{}

var errRead = errors.New("read failed")

func (errReader) Read([]byte) (int, error) { return 0, errRead }

func TestReadMemberSet(t *testing.T) {
	t.Parallel()

	bom := "\ufeff"

	tests := []struct {
		wantErr     error
		name        string
		in          string
		wantMembers []string
		wantAbsent  []string
		limits      segment.Limits
	}{
		{name: "lines", in: "a\nb\nc\n", wantMembers: []string{"a", "b", "c"}},
		{name: "crlf", in: "a\r\nb\r\n", wantMembers: []string{"a", "b"}, wantAbsent: []string{"a\r"}},
		{name: "blank lines", in: "\n\na\n\t\n \nb", wantMembers: []string{"a", "b"}},
		{
			name:        "trailing and leading spaces",
			in:          "  a \n\tb\t\n",
			wantMembers: []string{"a", "b"},
			wantAbsent:  []string{"  a "},
		},
		{name: "repeated rows are one member", in: "p\nq\np\nr\np\n", wantMembers: []string{"p", "q", "r"}},
		{name: "byte order mark", in: bom + "a\nb\n", wantMembers: []string{"a", "b"}, wantAbsent: []string{bom + "a"}},
		{
			name:        "byte order mark only on the first line",
			in:          "a\n" + bom + "b\n",
			wantMembers: []string{"a", bom + "b"},
		},
		{name: "inner spaces are kept", in: "a b\n", wantMembers: []string{"a b"}},
		{name: "no rows", in: "", wantMembers: nil},
		{name: "unicode", in: "é\n\u65e5\u672c\n", wantMembers: []string{"é", "\u65e5\u672c"}},
		{name: "line too long", in: strings.Repeat("a", 5000), wantErr: segment.ErrMemberTooLong},
		{
			name: "too many rows", in: "a\nb\nc\n", limits: segment.Limits{MaxMembers: 2},
			wantErr: segment.ErrTooManyMembers,
		},
		{
			name: "repeated rows count as rows", in: "p\nq\np\n", limits: segment.Limits{MaxMembers: 2},
			wantErr: segment.ErrTooManyMembers,
		},
		{
			name:        "the limit is inclusive",
			in:          "a\nb\n",
			limits:      segment.Limits{MaxMembers: 2},
			wantMembers: []string{"a", "b"},
		},
		{
			name: "too many bytes", in: strings.Repeat("abcd\n", 10), limits: segment.Limits{MaxBytes: 20},
			wantErr: segment.ErrMembersTooLarge,
		},
		{
			name:        "crlf lines count their two line end bytes",
			in:          "ab\r\ncd\r\n",
			limits:      segment.Limits{MaxBytes: 8},
			wantMembers: []string{"ab", "cd"},
		},
		{
			name:    "crlf file over the limit by one byte",
			in:      "ab\r\ncd\r\n",
			limits:  segment.Limits{MaxBytes: 7},
			wantErr: segment.ErrMembersTooLarge,
		},
		{
			name:        "last line without a line end is at the limit",
			in:          "ab\ncd",
			limits:      segment.Limits{MaxBytes: 5},
			wantMembers: []string{"ab", "cd"},
		},
		{
			name:    "last line without a line end over the limit",
			in:      "ab\ncd",
			limits:  segment.Limits{MaxBytes: 4},
			wantErr: segment.ErrMembersTooLarge,
		},
		{
			name:        "4096 bytes with LF",
			in:          strings.Repeat("a", 4096) + "\n",
			wantMembers: []string{strings.Repeat("a", 4096)},
		},
		{
			name:        "4096 bytes with CRLF",
			in:          strings.Repeat("a", 4096) + "\r\n",
			wantMembers: []string{strings.Repeat("a", 4096)},
		},
		{
			name:        "4096 bytes without a line end",
			in:          strings.Repeat("a", 4096),
			wantMembers: []string{strings.Repeat("a", 4096)},
		},
		{name: "4097 bytes with LF", in: strings.Repeat("a", 4097) + "\n", wantErr: segment.ErrMemberTooLong},
		{name: "4097 bytes with CRLF", in: strings.Repeat("a", 4097) + "\r\n", wantErr: segment.ErrMemberTooLong},
		{
			name:        "bytes within the limit",
			in:          "ab\ncd\n",
			limits:      segment.Limits{MaxBytes: 6},
			wantMembers: []string{"ab", "cd"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set, err := segment.ReadMemberSet(strings.NewReader(tt.in), tt.limits)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, set)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, len(distinct(tt.wantMembers)), set.Len())

			for _, member := range tt.wantMembers {
				assert.True(t, set.Contains(member), "member %q", member)
			}

			for _, absent := range tt.wantAbsent {
				assert.False(t, set.Contains(absent), "%q is not a member", absent)
			}
		})
	}

	_, err := segment.ReadMemberSet(errReader{}, segment.Limits{})
	require.ErrorIs(t, err, errRead)
}

func distinct(in []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range in {
		out[item] = struct{}{}
	}

	return out
}

func TestMemoryProviderFromReadSets(t *testing.T) {
	t.Parallel()

	beta, err := segment.ReadMemberSet(strings.NewReader("\ufeffalice\r\nbob\r\n"), segment.Limits{})
	require.NoError(t, err)

	provider := segment.NewMemoryProviderFromSets(map[string]*segment.MemberSet{segBeta: beta})
	lookups := []segment.Lookup{{Segment: segBeta, ID: "alice"}, {Segment: segBeta, ID: "carol"}}

	require.NoError(t, provider.Contains(context.Background(), lookups))
	assert.Equal(t, segment.Member, lookups[0].State)
	assert.Equal(t, segment.NotMember, lookups[1].State)
}

func TestStateAndName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "unknown", segment.Unknown.String())
	assert.Equal(t, "notMember", segment.NotMember.String())
	assert.Equal(t, "member", segment.Member.String())
	assert.Equal(t, "State(7)", segment.State(7).String())
	assert.True(t, segment.Member.Valid())
	assert.False(t, segment.State(3).Valid())

	names := []string{"a", "a-b_9", strings.Repeat("a", 64), "", "A", "a b", "\u00e9", strings.Repeat("a", 65)}
	for _, name := range names {
		assert.Equal(t, parser.ValidSegmentName(name), segment.ValidName(name), "name %q", name)
	}
}

// TestSegmentDoesNotPullTheParser pins the layering: a provider imports this package, and must not get the rules
// parser and its ANTLR runtime with it.
func TestSegmentDoesNotPullTheParser(t *testing.T) {
	t.Parallel()

	out, err := exec.CommandContext(context.Background(), "go", "list", "-deps", ".").Output()
	require.NoError(t, err)

	for dep := range strings.FieldsSeq(string(out)) {
		assert.NotContains(t, dep, "semaphore/rules", "the segment package must not depend on the rules engine")
		assert.NotContains(t, dep, "antlr")
	}
}

func TestResolveLeavesAnInvalidStateAlone(t *testing.T) {
	t.Parallel()

	def := segment.Definition{Name: "a", Type: segment.TypeFile, OnError: segment.OnErrorMember}
	assert.Equal(t, segment.State(9), def.Resolve(segment.State(9)), "only Unknown is resolved")

	unknownPolicy := segment.Definition{Name: "a", Type: segment.TypeFile, OnError: segment.OnError("other")}
	assert.Equal(t, segment.Unknown, unknownPolicy.Resolve(segment.Unknown), "an unrecognised policy keeps it unknown")
}
