package flagdef_test

import (
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	whereStep0 = "scheduled step 0"
	pathUserID = "user.id"
)

func TestScheduledStepSegmentsAreValidated(t *testing.T) {
	t.Parallel()

	step := func(rules []flagdef.Rule, defaultRule *flagdef.Rule) flagdef.ScheduledStep {
		patch := flagdef.InternalFlag{Rules: &rules, DefaultRule: defaultRule}

		return flagdef.ScheduledStep{InternalFlag: patch}
	}

	tests := []struct {
		name       string
		step       flagdef.ScheduledStep
		wantInErr  []string
		wantSegErr bool
	}{
		{
			name: "a query with a valid term",
			step: step([]flagdef.Rule{{Name: new("r"), Query: new(`k in segment("beta")`)}}, nil),
		},
		{
			name: "a query without a term is not checked",
			step: step([]flagdef.Rule{{Name: new("r"), Query: new(`k in [`)}}, nil),
		},
		{
			name:       "an invalid segment name",
			step:       step([]flagdef.Rule{{Name: new("r"), Query: new(`k in segment("Beta")`)}}, nil),
			wantInErr:  []string{whereStep0, `targeting rule "r" (index 0)`, errMsgBadName},
			wantSegErr: true,
		},
		{
			name: "an invalid jsonlogic term",
			step: step([]flagdef.Rule{
				{Name: new("a"), VariationResult: new(variantOn)},
				{Name: new("b"), Query: new(`{"some": [[1], {"inSegment": [{"var": "k"}, "a"]}]}`)},
			}, nil),
			wantInErr:  []string{whereStep0, `targeting rule "b" (index 1)`, `"some"`},
			wantSegErr: true,
		},
		{
			name:       "a segment term in the default rule of a step",
			step:       step(nil, &flagdef.Rule{Query: new(`k in segment("beta")`)}),
			wantInErr:  []string{whereStep0, "defaultRule", errMsgNotAllowed},
			wantSegErr: true,
		},
		{
			name:      "a jsonlogic query that does not decode is rejected like in a rule of the flag",
			step:      step([]flagdef.Rule{{Name: new("r"), Query: new(`{"==": [{"var": "k"}, }`)}}, nil),
			wantInErr: []string{whereStep0, errMsgBadJSONLogic},
		},
		{
			name:      "a malformed jsonlogic query after a space is rejected",
			step:      step([]flagdef.Rule{{Name: new("r"), Query: new(" " + `{"==": [{"var": "k"}, }`)}}, nil),
			wantInErr: []string{whereStep0, errMsgBadJSONLogic},
		},
		{
			name:      "a malformed jsonlogic query after a tab is rejected",
			step:      step([]flagdef.Rule{{Name: new("r"), Query: new("\t" + `{"==": [{"var": "k"}, }`)}}, nil),
			wantInErr: []string{whereStep0, errMsgBadJSONLogic},
		},
		{
			name:      "a malformed jsonlogic query after a carriage return is rejected",
			step:      step([]flagdef.Rule{{Name: new("r"), Query: new("\r" + `{"==": [{"var": "k"}, }`)}}, nil),
			wantInErr: []string{whereStep0, errMsgBadJSONLogic},
		},
		{
			name:      "a malformed jsonlogic query after a line feed is rejected",
			step:      step([]flagdef.Rule{{Name: new("r"), Query: new("\n" + `{"==": [{"var": "k"}, }`)}}, nil),
			wantInErr: []string{whereStep0, errMsgBadJSONLogic},
		},
		{
			name:      "a malformed jsonlogic query after a mixed whitespace is rejected",
			step:      step([]flagdef.Rule{{Name: new("r"), Query: new(" \t\r\n " + `{"==": [{"var": "k"}, }`)}}, nil),
			wantInErr: []string{whereStep0, errMsgBadJSONLogic},
		},
		{
			name:      "an invalid onError of a rule of a step",
			step:      step([]flagdef.Rule{{Name: new("r"), OnError: new("ignore")}}, nil),
			wantInErr: []string{whereStep0, `targeting rule "r" (index 0)`, errMsgBadOnError},
		},
		{
			name:      "an invalid onError of the default rule of a step",
			step:      step(nil, &flagdef.Rule{OnError: new("ignore")}),
			wantInErr: []string{whereStep0, "defaultRule", errMsgBadOnError},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flag := onOffFlag(queryRule("r", `age gt 1`))
			flag.Scheduled = &[]flagdef.ScheduledStep{tt.step}

			err := flag.IsValid(flagdef.NewService())
			if len(tt.wantInErr) == 0 {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)

			for _, want := range tt.wantInErr {
				assert.Contains(t, err.Error(), want)
			}

			if tt.wantSegErr {
				assert.ErrorIs(t, err, flagdef.ErrInvalidSegment)
			}
		})
	}
}

func TestSegmentOperandErrorsAreOnlyReportedWhenReached(t *testing.T) {
	t.Parallel()

	members := map[string][]string{segBeta: {keyAlice}}

	const (
		rulesOr  = `targetingKey in segment("beta") or user.id eq "staff"`
		rulesAnd = `targetingKey in segment("beta") and user.id eq "staff"`
		jsonOr   = `{"or": [{"inSegment": [{"var": "targetingKey"}, "beta"]}, ` +
			`{"map": [{"var": "targetingKey"}, {"var": ""}]}]}`
		jsonAnd = `{"and": [{"inSegment": [{"var": "targetingKey"}, "beta"]}, ` +
			`{"map": [{"var": "targetingKey"}, {"var": ""}]}]}`
		errorLog = "error while evaluating"

		// nested, with a decisive operand after: the error of the inner operand is the error of the rule
		nestedOr = `(targetingKey in segment("beta") or user.id eq "staff") or plan eq "pro"`
		//nolint:lll // a JsonLogic document on one line
		nestedJSONOr = `{"or": [{"or": [{"inSegment": [{"var": "targetingKey"}, "beta"]}, {"map": [{"var": "targetingKey"}, {"var": ""}]}]}, {"==": [{"var": "plan"}, "pro"]}]}`
		nestedAnd    = `(targetingKey in segment("beta") and user.id eq "staff") and plan eq "pro"`
		//nolint:lll // a JsonLogic document on one line
		nestedJSONAnd = `{"and": [{"and": [{"inSegment": [{"var": "targetingKey"}, "beta"]}, {"map": [{"var": "targetingKey"}, {"var": ""}]}]}, {"==": [{"var": "plan"}, "pro"]}]}`
	)

	tests := []struct {
		name        string
		query       string
		key         string
		wantVariant string
		wantLog     string
	}{
		{
			name:        "nested rules or: a member never reads the operand",
			query:       nestedOr,
			key:         keyAlice,
			wantVariant: variantOn,
		},
		{
			name:        "nested rules or: a non member reaches the error",
			query:       nestedOr,
			key:         keyBob,
			wantVariant: variantOff,
			wantLog:     errorLog,
		},
		{
			name:        "nested rules and: a non member never reads the operand",
			query:       nestedAnd,
			key:         keyBob,
			wantVariant: variantOff,
		},
		{
			name:        "nested rules and: a member reaches the error",
			query:       nestedAnd,
			key:         keyAlice,
			wantVariant: variantOff,
			wantLog:     errorLog,
		},
		{
			name:        "nested jsonlogic or: a member never reads the operand",
			query:       nestedJSONOr,
			key:         keyAlice,
			wantVariant: variantOn,
		},
		{
			name:        "nested jsonlogic or: a non member reaches the error",
			query:       nestedJSONOr,
			key:         keyBob,
			wantVariant: variantOff,
			wantLog:     errorLog,
		},
		{
			name:        "nested jsonlogic and: a non member never reads the operand",
			query:       nestedJSONAnd,
			key:         keyBob,
			wantVariant: variantOff,
		},
		{
			name:        "nested jsonlogic and: a member reaches the error",
			query:       nestedJSONAnd,
			key:         keyAlice,
			wantVariant: variantOff,
			wantLog:     errorLog,
		},
		{name: "rules or: a member never reads the operand", query: rulesOr, key: keyAlice, wantVariant: variantOn},
		{
			name:        "rules or: a non member reaches the error",
			query:       rulesOr,
			key:         keyBob,
			wantVariant: variantOff,
			wantLog:     errorLog,
		},
		{
			name:        "rules and: a non member never reads the operand",
			query:       rulesAnd,
			key:         keyBob,
			wantVariant: variantOff,
		},
		{
			name:        "rules and: a member reaches the error",
			query:       rulesAnd,
			key:         keyAlice,
			wantVariant: variantOff,
			wantLog:     errorLog,
		},
		{name: "jsonlogic or: a member never reads the operand", query: jsonOr, key: keyAlice, wantVariant: variantOn},
		{
			name:        "jsonlogic or: a non member reaches the error",
			query:       jsonOr,
			key:         keyBob,
			wantVariant: variantOff,
			wantLog:     errorLog,
		},
		{
			name:        "jsonlogic and: a non member never reads the operand",
			query:       jsonAnd,
			key:         keyBob,
			wantVariant: variantOff,
		},
		{
			name:        "jsonlogic and: a member reaches the error",
			query:       jsonAnd,
			key:         keyAlice,
			wantVariant: variantOff,
			wantLog:     errorLog,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf syncBuffer

			svc := flagdef.NewService(flagdef.WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			rec := &recordingProvider{inner: segment.NewMemoryProvider(members)}
			flag := onOffFlag(queryRule("r", tt.query))
			require.NoError(t, flag.IsValid(svc))

			evalCtx := ffcontext.NewEvaluationContextBuilder(tt.key).
				AddCustom(ownedUserKey, "not-an-object").AddCustom("plan", planPro).Build()
			_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: rec})

			assert.Equal(t, tt.wantVariant, details.Variant)
			assert.Equal(t, [][]string{{"beta:" + tt.key}}, rec.calls, "the membership is asked first")

			if tt.wantLog == "" {
				assert.NotContains(t, buf.String(), "level=ERROR")
			} else {
				assert.Contains(t, buf.String(), tt.wantLog)
			}
		})
	}
}

type userID struct {
	ID string `json:"id"`
}

type scored struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

type cycle struct {
	*cycle

	ID string `json:"id"`
}

func newCycle() *cycle {
	c := &cycle{ID: keyAlice}
	c.cycle = c

	return c
}

type loop struct {
	Next *loop  `json:"next"`
	ID   string `json:"id"`
}

func newLoop() *loop {
	l := &loop{ID: keyAlice}
	l.Next = l

	return l
}

type deeper struct {
	ID string `json:"id"`
}

type shallow struct {
	deeper

	ID string `json:"id"`
}

type first struct {
	ID string
}

type second struct {
	ID string
}

type sameDepth struct {
	first
	second
}

type plainID struct {
	ID string
}

type taggedID struct {
	Other string `json:"ID"` //nolint:tagliatelle // the tie of names needs the exact name ID
}

type taggedTie struct {
	plainID
	taggedID
}

type embedsTime struct {
	*time.Time

	ID string `json:"id"`
}

type panicsOnMarshal struct{}

func (panicsOnMarshal) MarshalJSON() ([]byte, error) { panic(secretID) }

type withMarshaler struct {
	Inner *marshaled `json:"inner"`
}

type marshaled struct{}

//nolint:unparam // the interface needs the error
func (marshaled) MarshalJSON() ([]byte, error) { return []byte(`{"id": "alice"}`), nil }

type hiddenFields struct {
	id      string
	Skipped string `json:"-"`
	Renamed string `json:"uid"`
}

type base struct {
	ID string `json:"id"`
}

type embeds struct{ base }

type embedsPointer struct{ *base }

type withPointer struct {
	User *userID `json:"user"`
}

type numbered struct {
	N int64 `json:"n"`
}

type userName string

type profile struct {
	User userID `json:"user"`
}

func TestSegmentAttributesOfTypedObjects(t *testing.T) {
	t.Parallel()

	members := map[string][]string{
		segIDs: {keyAlice, "9007199254740993", "12345", "1", "1000", "123456789012345678901234567890", "100000010"},
	}

	tests := []struct {
		attr        any
		siblings    map[string]any // other attributes of the context
		name        string
		attrName    string
		path        string
		wantVariant string
		wantLookups [][]string
	}{
		{
			name: "map of strings", attr: map[string]string{"id": keyAlice}, attrName: ownedUserKey, path: pathUserID,
			wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name: "struct with json tags", attr: userID{ID: keyAlice}, attrName: ownedUserKey, path: pathUserID,
			wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name: "pointer to a struct", attr: &userID{ID: keyAlice}, attrName: ownedUserKey, path: pathUserID,
			wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name:        "struct holding a struct",
			attr:        profile{User: userID{ID: keyAlice}},
			attrName:    "profile",
			path:        "profile.user.id",
			wantLookups: [][]string{{lookupIDsAlice}},
			wantVariant: variantOn,
		},
		{
			name: "map of any", attr: map[string]any{"id": keyAlice}, attrName: ownedUserKey, path: pathUserID,
			wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name:        "typed map without the key",
			attr:        map[string]string{"name": "x"},
			attrName:    ownedUserKey,
			path:        pathUserID,
			wantVariant: variantOff,
		},
		{
			name:        "NaN in the same object makes the attribute unreadable",
			attr:        scored{ID: keyAlice, Score: math.NaN()},
			attrName:    ownedUserKey,
			path:        pathUserID,
			wantVariant: variantOff,
		},
		{
			name: "NaN in another attribute does not matter", attr: userID{ID: keyAlice},
			siblings: map[string]any{"score": math.NaN(), "channel": make(chan int)}, attrName: ownedUserKey,
			path: pathUserID, wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name: "a cycle of embedded pointers does not crash", attr: newCycle(),
			attrName: ownedUserKey, path: pathUserID, wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name: "a cycle that cannot be encoded is an unreadable attribute", attr: newLoop(),
			attrName: ownedUserKey, path: pathUserID, wantVariant: variantOff,
		},
		{
			name: "the shallower of two names wins", attr: shallow{ID: keyAlice, deeper: deeper{ID: segOther}},
			attrName: ownedUserKey, path: pathUserID, wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name:        "two names at the same depth are both omitted",
			attr:        sameDepth{first: first{ID: keyAlice}, second: second{ID: keyAlice}},
			attrName:    ownedUserKey,
			path:        "user.ID",
			wantVariant: variantOff,
		},
		{
			name:        "a tagged name wins a tie with an untagged one",
			attr:        taggedTie{plainID: plainID{ID: segOther}, taggedID: taggedID{Other: keyAlice}},
			attrName:    ownedUserKey,
			path:        "user.ID",
			wantLookups: [][]string{{lookupIDsAlice}},
			wantVariant: variantOn,
		},
		{
			name: "a json.Number integer", attr: map[string]any{"id": json.Number("12345")}, attrName: ownedUserKey,
			path: pathUserID, wantLookups: [][]string{{"ids:12345"}}, wantVariant: variantOn,
		},
		{
			name: "a json.Number written 1.0", attr: map[string]any{"id": json.Number("1.0")}, attrName: ownedUserKey,
			path: pathUserID, wantLookups: [][]string{{"ids:1"}}, wantVariant: variantOn,
		},
		{
			name: "a json.Number written 1e3", attr: map[string]any{"id": json.Number("1e3")}, attrName: ownedUserKey,
			path: pathUserID, wantLookups: [][]string{{"ids:1000"}}, wantVariant: variantOn,
		},
		{
			name: "a json.Number with a fraction is not an identifier", attr: map[string]any{"id": json.Number("1.5")},
			attrName: ownedUserKey, path: pathUserID, wantVariant: variantOff,
		},
		{
			name: "a json.Number beyond any float is exact", attr: struct {
				ID json.Number `json:"id"`
			}{ID: json.Number("123456789012345678901234567890")},
			attrName: ownedUserKey, path: pathUserID, wantLookups: [][]string{{"ids:123456789012345678901234567890"}},
			wantVariant: variantOn,
		},
		{
			name: "a json.Number with a huge exponent is not read", attr: map[string]any{"id": json.Number("1e99999")},
			attrName: ownedUserKey, path: pathUserID, wantVariant: variantOff,
		},
		{
			name: "a nil embedded pointer whose type has MarshalJSON does not crash", attr: embedsTime{ID: keyAlice},
			attrName: ownedUserKey, path: pathUserID, wantVariant: variantOff,
		},
		{
			name: "a MarshalJSON that panics makes the attribute unreadable", attr: panicsOnMarshal{},
			attrName: ownedUserKey, path: pathUserID, wantVariant: variantOff,
		},
		{
			name: "a nil pointer to a marshaler in the middle of the path is missing", attr: withMarshaler{},
			attrName: ownedUserKey, path: "user.inner.id", wantVariant: variantOff,
		},
		{
			name: "a float32 is what JSON prints for it",
			attr: struct {
				ID float32 `json:"id"`
			}{ID: 100000008},
			attrName:    ownedUserKey,
			path:        pathUserID,
			wantLookups: [][]string{{"ids:100000010"}},
			wantVariant: variantOn,
		},
		{
			name: "a json.Marshaler decides the names", attr: marshaled{}, attrName: ownedUserKey,
			path: pathUserID, wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name: "an unexported field is missing", attr: hiddenFields{id: keyAlice}, attrName: ownedUserKey,
			path: pathUserID, wantVariant: variantOff,
		},
		{
			name: "a field tagged json dash is missing", attr: hiddenFields{Skipped: keyAlice}, attrName: ownedUserKey,
			path: "user.Skipped", wantVariant: variantOff,
		},
		{
			name:        "a renamed field is read by its json name",
			attr:        hiddenFields{Renamed: keyAlice},
			attrName:    ownedUserKey,
			path:        "user.uid",
			wantLookups: [][]string{{lookupIDsAlice}},
			wantVariant: variantOn,
		},
		{
			name:        "a renamed field is not read by its Go name",
			attr:        hiddenFields{Renamed: keyAlice},
			attrName:    ownedUserKey,
			path:        "user.Renamed",
			wantVariant: variantOff,
		},
		{
			name: "an embedded struct is promoted", attr: embeds{base: base{ID: keyAlice}}, attrName: ownedUserKey,
			path: pathUserID, wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name: "an embedded pointer is promoted", attr: embedsPointer{base: &base{ID: keyAlice}},
			attrName: ownedUserKey, path: pathUserID, wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name: "a nil embedded pointer is missing", attr: embedsPointer{}, attrName: ownedUserKey,
			path: pathUserID, wantVariant: variantOff,
		},
		{
			name: "a nil pointer in the middle of the path is missing", attr: withPointer{}, attrName: "profile",
			path: "profile.user.id", wantVariant: variantOff,
		},
		{
			name: "a named string type", attr: map[string]userName{"id": keyAlice}, attrName: ownedUserKey,
			path: pathUserID, wantLookups: [][]string{{lookupIDsAlice}}, wantVariant: variantOn,
		},
		{
			name: "an integer above 2^53 is exact", attr: numbered{N: 9007199254740993}, attrName: ownedUserKey,
			path: "user.n", wantLookups: [][]string{{"ids:9007199254740993"}}, wantVariant: variantOn,
		},
		{
			name: "a list is not an identifier", attr: map[string]any{"id": []string{keyAlice}}, attrName: ownedUserKey,
			path: pathUserID, wantVariant: variantOff,
		},
		{
			name: "a scalar where an object is expected", attr: 42, attrName: ownedUserKey, path: pathUserID,
			wantVariant: variantOff,
		},
	}

	for _, tt := range tests {
		for format, query := range map[string]string{
			formatNikunjy: tt.path + ` in segment("ids")`,
			formatJSON:    `{"inSegment": [{"var": "` + tt.path + `"}, "ids"]}`,
		} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				t.Parallel()

				svc := flagdef.NewService()
				rec := &recordingProvider{inner: segment.NewMemoryProvider(members)}
				flag := onOffFlag(queryRule("r", query))
				require.NoError(t, flag.IsValid(svc))

				builder := ffcontext.NewEvaluationContextBuilder("k").AddCustom(tt.attrName, tt.attr)
				for key, value := range tt.siblings {
					builder.AddCustom(key, value)
				}

				_, details := flag.Value("f", builder.Build(), flagdef.Context{Service: svc, Segments: rec})

				assert.Equal(t, tt.wantVariant, details.Variant)
				assert.Equal(t, tt.wantLookups, rec.calls)
			})
		}
	}
}

func TestDisabledSegmentRuleIsNotLookedUp(t *testing.T) {
	t.Parallel()

	for format, query := range map[string]string{formatNikunjy: nikunjyInclude, formatJSON: jsonInclude} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			rec := &recordingProvider{inner: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})}
			rule := queryRule("r", query)
			rule.Disable = new(true)
			flag := onOffFlag(rule)
			require.NoError(t, flag.IsValid(svc))

			_, details := flag.Value("f", ffcontext.NewEvaluationContext(keyAlice),
				flagdef.Context{Service: svc, Segments: rec})

			assert.Equal(t, variantOff, details.Variant, "a disabled rule never applies")
			assert.Empty(t, rec.calls, "and does not look a segment up")
			assert.Equal(t, segment.StatusNone, details.SegmentStatus)
			assert.True(t, details.Cacheable)
		})
	}
}

func TestSegmentTermsAreCopies(t *testing.T) {
	t.Parallel()

	svc := flagdef.NewService()
	flag := onOffFlag(queryRule("r", `user.id in segment("ids")`))
	require.NoError(t, flag.IsValid(svc))

	first := flag.SegmentTerms(svc)
	require.Len(t, first, 1)
	first[0].Path[0] = changedName
	first[0].Name = changedName

	assert.Equal(t, []segment.Term{{Name: segIDs, Path: []string{ownedUserKey, "id"}}}, flag.SegmentTerms(svc))

	rec := &recordingProvider{inner: segment.NewMemoryProvider(map[string][]string{segIDs: {keyAlice}})}
	evalCtx := ffcontext.NewEvaluationContextBuilder("k").
		AddCustom(ownedUserKey, map[string]any{"id": keyAlice}).
		Build()
	_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: rec})

	assert.Equal(t, variantOn, details.Variant, "the evaluation does not read what the caller changed")
}

func TestSegmentErrorOffsetIsInTheQueryTheEngineParses(t *testing.T) {
	t.Parallel()

	// the engine joins the lines of a query with a space and removes the leading blanks of each line before it parses it
	query := "age gt 1\n    and key in segment(\"Bad\")"
	joined := "age gt 1 and key in segment(\"Bad\")"

	err := onOffFlag(queryRule("r", query)).IsValid(flagdef.NewService())
	require.ErrorIs(t, err, flagdef.ErrInvalidSegment)
	assert.Contains(t, err.Error(), "at offset "+strconv.Itoa(strings.Index(joined, "Bad")))
}

func TestSegmentOperandErrorBeforeTheFirstSegmentIsTheErrorOfTheRule(t *testing.T) {
	t.Parallel()

	queries := map[string]string{
		formatNikunjy: `user.id eq "staff" or targetingKey in segment("beta")`,
		// the failing comparison is recorded, not raised: the segment term is reached after it
		formatNikunjy + " (recorded error)": `plan gt true or targetingKey in segment("beta")`,
		formatJSON: `{"or": [{"map": [{"var": "targetingKey"}, {"var": ""}]}, ` +
			`{"inSegment": [{"var": "targetingKey"}, "beta"]}]}`,
	}

	for format, query := range queries {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			var buf syncBuffer

			svc := flagdef.NewService(flagdef.WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			rec := &recordingProvider{inner: segment.NewMemoryProvider(map[string][]string{segBeta: {keyAlice}})}
			flag := onOffFlag(queryRule("r", query))
			require.NoError(t, flag.IsValid(svc))

			evalCtx := ffcontext.NewEvaluationContextBuilder(keyAlice).
				AddCustom(ownedUserKey, "not-an-object").
				AddCustom("plan", planPro).
				Build()
			_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: rec})

			assert.Equal(t, variantOff, details.Variant, "the member does not match: the rule fails first")
			assert.Empty(t, rec.calls, "the segment is never reached, nothing is looked up")
			assert.Contains(t, buf.String(), "error while evaluating")
		})
	}
}

// An unknown term does not stop the evaluation and an error is not an unknown result: the operand after an unknown
// term is read, and when it fails the rule fails, whatever onError says.
func TestSegmentUnknownTermDoesNotHideAnOperandError(t *testing.T) {
	t.Parallel()

	queries := map[string]string{
		formatNikunjy: `targetingKey in segment("beta") or user.id eq "staff"`,
		formatJSON: `{"or": [{"inSegment": [{"var": "targetingKey"}, "beta"]}, ` +
			`{"map": [{"var": "targetingKey"}, {"var": ""}]}]}`,
	}

	for format, query := range queries {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			rule := queryRule("r", query)
			rule.OnError = new(ruleOnErrorMatch)
			flag := onOffFlag(rule)
			require.NoError(t, flag.IsValid(svc))

			evalCtx := ffcontext.NewEvaluationContextBuilder(keyAlice).AddCustom(ownedUserKey, "not-an-object").Build()
			_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: failingProvider{}})

			assert.Equal(
				t,
				variantOff,
				details.Variant,
				"an error is not an unknown result: onError match does not apply",
			)
			assert.Equal(t, segment.StatusStoreError, details.SegmentStatus)
		})
	}
}

// countingObject counts how many times it is converted to JSON.
type countingObject struct {
	conversions *atomic.Int32
	id          string
	email       string
}

//nolint:unparam // the interface needs the error
func (c countingObject) MarshalJSON() ([]byte, error) {
	c.conversions.Add(1)

	return []byte(`{"id": "` + c.id + `", "email": "` + c.email + `"}`), nil
}

// A typed object of the context is converted once for each evaluation, whatever the number of terms that read it, and
// nothing is kept for the next evaluation.
func TestTypedAttributeIsConvertedOncePerEvaluation(t *testing.T) {
	t.Parallel()

	queries := map[string]string{
		formatNikunjy: `user.id in segment("ids") and user.email in segment("emails") and profile.id in segment("ids")`,
		formatJSON: `{"and": [{"inSegment": [{"var": "user.id"}, "ids"]}, ` +
			`{"inSegment": [{"var": "user.email"}, "emails"]}, {"inSegment": [{"var": "profile.id"}, "ids"]}]}`,
	}

	for format, query := range queries {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			provider := segment.NewMemoryProvider(map[string][]string{segIDs: {keyAlice}, "emails": {"a@b.c"}})
			flag := onOffFlag(queryRule("r", query))
			require.NoError(t, flag.IsValid(svc))

			var user, profile atomic.Int32

			evalCtx := ffcontext.NewEvaluationContextBuilder("k").
				AddCustom(ownedUserKey, countingObject{conversions: &user, id: keyAlice, email: "a@b.c"}).
				AddCustom("profile", countingObject{conversions: &profile, id: keyAlice}).Build()

			for evaluation := int32(1); evaluation <= 2; evaluation++ {
				_, details := flag.Value("f", evalCtx, flagdef.Context{Service: svc, Segments: provider})

				assert.Equal(t, variantOn, details.Variant)
				assert.Equal(t, evaluation, user.Load(), "one conversion of user for each evaluation")
				assert.Equal(t, evaluation, profile.Load(), "one conversion of profile for each evaluation")
			}
		})
	}
}
