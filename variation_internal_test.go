package ffclient

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thejerf/slogassert"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/logsexporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/model/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	boolVariationDetails         = "BoolVariationDetails"
	callVariationBeforeInit      = "Call variation before init of SDK"
	defaultNotkey                = "default-notkey"
	disableFlag                  = "disable-flag"
	bracketPrefixPattern         = "^\\["
	getDefaultValueIf            = "Get default value if flag disable"
	getDefaultValueRule          = "Get default value, rule not apply"
	getDefaultValueWhen          = "Get default value, when rule apply and not right type"
	getDefaultValueWith          = "Get default value with key not exist"
	getErrorWhenCache            = "Get error when cache not init"
	getFalseValueRule            = "Get false value, rule apply"
	getSdkDefaultValue           = "Get sdk default value if offline"
	getTrueValueRule             = "Get true value, rule apply"
	intVariation                 = "IntVariation"
	jsonArrayVariation           = "JSONArrayVariation"
	jsonArrayVariationGotV       = "JSONArrayVariation() got = %v, want %v"
	keyNotExist                  = "key-not-exist"
	noExportedLog                = "No exported log"
	randomKeySsssOne             = "random-key-ssss1"
	staging                      = "staging"
	test123                      = "test123"
	testTwo                      = "test2"
	testThree                    = "test3"
	toLogSuffix                  = "to\\]\"\n"
	userRandomKeyFlag            = "\\] user=\"random-key\", flag=\"key-not-exist\", value=\"\\[to"
	logDisableFlagBool           = `user="random-key", flag="disable-flag", value="true", variation="SdkDefault"`
	logKeyNotExistFloat          = `user="random-key", flag="key-not-exist", value="118.12", variation="SdkDefault"`
	logKeyNotExistBool           = `user="random-key", flag="key-not-exist", value="true", variation="SdkDefault"`
	logTestFlagInt               = `user="random-key", flag="test-flag", value="120", variation="True"`
	logTestFlagBool              = `user="random-key", flag="test-flag", value="true", variation="True"`
	userRandomKeySsss            = `user="random-key-ssss1", flag="test-flag", value="false", variation="False"`
	userUserKeyFlagKey           = "user=\"{{ .UserKey}}\", flag=\"{{ .Key}}\", "
	valueValueVariationVariation = "value=\"{{ .Value}}\", variation=\"{{ .Variation}}\""
	yaml                         = "YAML"
)

var (
	errImpossibleToReadThe = errors.New("impossible to read the toggle before the initialisation")
	errFlagKeyNotExist     = errors.New("flag [key-not-exist] does not exists")
)

type cacheMock struct {
	flag flag.Flag
	err  error
}

type variationArgs[T any] struct {
	defaultValue  T
	evaluationCtx ffcontext.Context
	cacheMock     cache.Manager
	flagKey       string
	offline       bool
	disableInit   bool
}

type variationCase[T any, R any] struct {
	name        string
	expectedLog string
	want        R
	args        variationArgs[T]
	wantErr     bool
}

func NewCacheMock(flg flag.Flag, err error) *cacheMock {
	return &cacheMock{
		flag: flg,
		err:  err,
	}
}

func (*cacheMock) GetLatestUpdateDate() time.Time {
	return clocktest.Instant()
}

func (*cacheMock) ConvertToFlagStruct(_ []byte, _ string) (map[string]dto.DTO, error) {
	return nil, assert.AnError
}
func (*cacheMock) UpdateCache(_ map[string]dto.DTO, _ *slog.Logger, _ bool) error {
	return nil
}

func (*cacheMock) Close() {}
func (c *cacheMock) GetFlag(_ string) (flag.Flag, error) {
	return c.flag, c.err
}

func (*cacheMock) AllFlags() (map[string]flag.Flag, error) { return nil, assert.AnError }

func (*cacheMock) AllFlagsReadOnly() (map[string]flag.Flag, error) { return nil, assert.AnError }

// assertExpectedLog waits for the async logger to flush and asserts that a log
// message containing expectedLog was emitted. It is a no-op when expectedLog is
// empty.
//
// The log is emitted from a goroutine, so we poll instead of sleeping for a fixed
// duration: a flat wait is a race on a loaded machine (the Windows timer granularity
// alone is ~15ms). handler.Assert is mutex-protected, returns the number of matches
// without failing, and only consumes the messages it matched, so it is safe to call
// repeatedly from the require.Eventually goroutine. handler.Fail must NOT be called
// from there: it calls t.Fatalf, which is illegal outside the test goroutine.
func assertExpectedLog(t *testing.T, handler *slogassert.Handler, expectedLog string) {
	t.Helper()

	if expectedLog == "" {
		return
	}

	require.Eventually(t, func() bool {
		return handler.Assert(func(message slogassert.LogMessage) bool {
			// the logs exporter logs the formatted event as the "event" attribute
			return strings.Contains(message.Message, expectedLog) ||
				strings.Contains(message.Attrs["event"].String(), expectedLog)
		}) > 0
	}, 2*time.Second, 20*time.Millisecond,
		"no log message containing %q was emitted", expectedLog)
}

func newVariationTestClient(
	logger *slog.Logger,
	cacheMock cache.Manager,
	offline bool,
) *GoFeatureFlag {
	mgrCfg := retriever.ManagerConfig{
		FileFormat:              yaml,
		DisableNotifierOnInit:   false,
		StartWithRetrieverError: false,
		EnablePollingJitter:     true,
		PollingInterval:         500,
	}
	ffLogger := logger

	return &GoFeatureFlag{
		evaluationService: flag.NewService(),
		retrieverManager:  retriever.NewManager(mgrCfg, []retriever.Retriever{}, cacheMock, ffLogger),
		config: Config{
			PollingInterval: 0,
			LeveledLogger:   logger,
			Offline:         offline,
		},
		featureEventDataExporter: exporter.NewManager[exporter.FeatureEvent](
			[]exporter.Config{{
				FlushInterval:    0,
				MaxEventInMemory: 0,
				Exporter: &logsexporter.Exporter{
					LogFormat: userUserKeyFlagKey +
						valueValueVariationVariation,
				},
			}},
			exporter.DefaultExporterCleanQueueInterval,
			ffLogger,
		),
	}
}

func runVariationTests[T any, R any](
	t *testing.T,
	label string,
	evalFn func(*GoFeatureFlag, string, ffcontext.Context, T) (R, error),
	tests []variationCase[T, R],
) {
	t.Helper()

	runVariationWithCheck(t, label, evalFn, false, false, func(t *testing.T, want, got R) {
		t.Helper()
		assert.Equal(t, want, got, "%s() got = %v, want %v", label, got, want)
	}, tests)
}

func runVariationWithCheck[T any, R any](
	t *testing.T,
	label string,
	evalFn func(*GoFeatureFlag, string, ffcontext.Context, T) (R, error),
	logAfterSuccess bool,
	alwaysAssertResult bool,
	assertGot func(t *testing.T, want, got R),
	tests []variationCase[T, R],
) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := slogassert.New(t, slog.LevelInfo, nil)

			var ff *GoFeatureFlag
			if !tt.args.disableInit {
				ff = newVariationTestClient(slog.New(handler), tt.args.cacheMock, tt.args.offline)
			}

			got, err := evalFn(ff, tt.args.flagKey, tt.args.evaluationCtx, tt.args.defaultValue)
			if !logAfterSuccess {
				assertExpectedLog(t, handler, tt.expectedLog)
			}

			if tt.wantErr && !alwaysAssertResult {
				assert.Error(t, err, "%s() error = %v, wantErr %v", label, err, tt.wantErr)

				return
			}

			if tt.wantErr {
				require.Error(t, err, "%s() error = %v, wantErr %v", label, err, tt.wantErr)
			}

			assertGot(t, tt.want, got)

			if logAfterSuccess {
				assertExpectedLog(t, handler, tt.expectedLog)
			}
		})
	}
}

func TestBoolVariation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[bool]

	runVariationTests(t, "BoolVariation", (*GoFeatureFlag).BoolVariation, []variationCase[bool, bool]{
		{
			name: callVariationBeforeInit,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  false,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Variations: &map[string]*any{
						fixtures.VarTrue:  new(any(true)),
						fixtures.VarFalse: new(any(false)),
					},
					Rules: &[]flag.Rule{{
						Query:       new(fixtures.QueryKeyEqKey),
						Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
					},
					BucketingKey: nil,
					DefaultRule: &flag.Rule{
						VariationResult: new(fixtures.VarFalse),
					},
				}, nil),
				disableInit: true,
			},
			want:    false,
			wantErr: true,
		},
		{
			name: getDefaultValueIf,
			args: args{
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want:        true,
			wantErr:     false,
			expectedLog: logDisableFlagBool,
		},
		{
			name: getErrorWhenCache,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  true,
				cacheMock: NewCacheMock(
					&flag.InternalFlag{},
					errImpossibleToReadThe),
			},
			want:        true,
			wantErr:     true,
			expectedLog: logKeyNotExistBool,
		},
		{
			name: getDefaultValueWith,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  true,
				cacheMock: NewCacheMock(
					&flag.InternalFlag{},
					errFlagKeyNotExist,
				),
			},
			want:        true,
			wantErr:     true,
			expectedLog: logKeyNotExistBool,
		},
		{
			name: getDefaultValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(true)),
						fixtures.VariationFalse:   new(any(false)),
						fixtures.VariationTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        true,
			wantErr:     false,
			expectedLog: `user="random-key", flag="test-flag", value="true", variation="Default"`,
		},
	})
}

func TestBoolVariation_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[bool]

	runVariationTests(t, "BoolVariation", (*GoFeatureFlag).BoolVariation, []variationCase[bool, bool]{
		{
			name: getTrueValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(false)),
						fixtures.VariationFalse:   new(any(false)),
						fixtures.VariationTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        true,
			wantErr:     false,
			expectedLog: logTestFlagBool,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(randomKeySsssOne),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.AnonymousEqTrue),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 90,
								fixtures.VariationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(false)),
						fixtures.VariationFalse:   new(any(false)),
						fixtures.VariationTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        false,
			wantErr:     false,
			expectedLog: userRandomKeySsss,
		},
		{
			name: getDefaultValueWhen,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.PlaceholderXxx)),
						fixtures.VariationFalse:   new(any(fixtures.PlaceholderXxx)),
						fixtures.VariationTrue:    new(any(fixtures.PlaceholderXxx)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        true,
			wantErr:     true,
			expectedLog: `user="random-key-ssss1", flag="test-flag", value="true", variation="SdkDefault"`,
		},
		{
			name: noExportedLog,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(false)),
						fixtures.VariationFalse:   new(any(false)),
						fixtures.VariationTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
					TrackEvents: new(false),
				}, nil),
			},
			want:        true,
			wantErr:     false,
			expectedLog: "",
		},
		{
			name: getSdkDefaultValue,
			args: args{
				offline:       true,
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  false,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want:        false,
			wantErr:     false,
			expectedLog: "",
		},
	})
}

func TestBoolVariationDetails(t *testing.T) {
	t.Parallel()

	type args = variationArgs[bool]

	runVariationTests(
		t,
		boolVariationDetails,
		(*GoFeatureFlag).BoolVariationDetails,
		[]variationCase[bool, model.VariationResult[bool]]{
			{
				name: callVariationBeforeInit,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  false,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VarTrue:  new(any(true)),
							fixtures.VarFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarFalse),
						},
					}, nil),
					disableInit: true,
				},
				wantErr: true,
			},
			{
				name: getDefaultValueIf,
				args: args{
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonDisabled,
					Value:         true,
					TrackEvents:   true,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: logDisableFlagBool,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  true,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				wantErr:     true,
				expectedLog: logKeyNotExistBool,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  true,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				wantErr:     true,
				expectedLog: logKeyNotExistBool,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Reason:        flag.ReasonDefault,
					Value:         true,
					TrackEvents:   true,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="true", variation="Default"`,
			},
		})
}

func TestBoolVariationDetails_RuleMetadata(t *testing.T) {
	t.Parallel()

	type args = variationArgs[bool]

	runVariationTests(
		t,
		boolVariationDetails,
		(*GoFeatureFlag).BoolVariationDetails,
		[]variationCase[bool, model.VariationResult[bool]]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: fixtures.VariationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         true,
					TrackEvents:   true,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: logTestFlagBool,
			},
			{
				name: "Get rule name on metadata, rule apply",
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: fixtures.VariationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         true,
					TrackEvents:   true,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: logTestFlagBool,
			},
			{
				name: "Get no rule name on metadata, rule apply has not name",
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: fixtures.VariationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         true,
					TrackEvents:   true,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: logTestFlagBool,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(randomKeySsssOne),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.AnonymousEqTrue),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 90,
									fixtures.VariationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: fixtures.VariationFalse,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatchSplit,
					Value:         false,
					TrackEvents:   true,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: userRandomKeySsss,
			},
		})
}

func TestBoolVariationDetails_FallbackAndBucketing(t *testing.T) {
	t.Parallel()

	type args = variationArgs[bool]

	runVariationTests(
		t,
		boolVariationDetails,
		(*GoFeatureFlag).BoolVariationDetails,
		[]variationCase[bool, model.VariationResult[bool]]{
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationFalse:   new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationTrue:    new(any(fixtures.PlaceholderXxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: flag.VariationSDKDefault,
					Failed:        true,
					Reason:        flag.ReasonError,
					ErrorCode:     flag.ErrorCodeTypeMismatch,
					Value:         false,
					TrackEvents:   true,
				},
				wantErr:     true,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="true", variation="SdkDefault"`,
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  false,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonOffline,
					Value:         false,
					TrackEvents:   false,
				},
				wantErr:     false,
				expectedLog: "",
			},
			{
				name: "Evaluation without targetingKey: success",
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(""),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							"on":                  new(any(true)),
							fixtures.VariationOff: new(any(false)),
						},
						DefaultRule: &flag.Rule{
							VariationResult: new("on"),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: "on",
					Failed:        false,
					Reason:        flag.ReasonStatic,
					Value:         true,
					TrackEvents:   true,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="", flag="test-flag", value="true", variation="on"`,
			},
			{
				name: "Evaluation without targetingKey: fail because flag requires bucketing",
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(""),
					defaultValue:  false,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							"on":                  new(any(true)),
							fixtures.VariationOff: new(any(false)),
						},
						DefaultRule: &flag.Rule{
							Percentages: &map[string]float64{
								"on":                  100,
								fixtures.VariationOff: 0,
							},
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: "SdkDefault",
					Failed:        true,
					Reason:        flag.ReasonError,
					ErrorCode:     flag.ErrorCodeTargetingKeyMissing,
					ErrorDetails:  "Error: Empty targeting key",
					Value:         false,
					TrackEvents:   true,
					Cacheable:     false,
				},
				wantErr:     false,
				expectedLog: `user="", flag="test-flag", value="false", variation="SdkDefault"`,
			},
		})
}

func TestFloat64Variation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[float64]

	assertFloat64 := func(t *testing.T, want, got float64) {
		t.Helper()
		assert.InDelta(t, want, got, 0, "Float64Variation() got = %v, want %v", got, want)
	}
	runVariationWithCheck(
		t,
		"Float64Variation",
		(*GoFeatureFlag).Float64Variation,
		false,
		false,
		assertFloat64,
		[]variationCase[float64, float64]{
			{
				name: callVariationBeforeInit,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  123.3,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VarTrue:  new(any(true)),
							fixtures.VarFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarFalse),
						},
					}, nil),
					disableInit: true,
				},
				want:    123.3,
				wantErr: true,
			},
			{
				name: getDefaultValueIf,
				args: args{
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  120.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want:        120.12,
				wantErr:     false,
				expectedLog: `user="random-key", flag="disable-flag", value="120.12", variation="SdkDefault"`,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				want:        118.12,
				wantErr:     true,
				expectedLog: logKeyNotExistFloat,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				want:        118.12,
				wantErr:     true,
				expectedLog: logKeyNotExistFloat,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119.12)),
							fixtures.VariationFalse:   new(any(121.12)),
							fixtures.VariationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        119.12,
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="119.12", variation="Default"`,
			},
		})
}

func TestFloat64Variation_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[float64]

	assertFloat64 := func(t *testing.T, want, got float64) {
		t.Helper()
		assert.InDelta(t, want, got, 0, "Float64Variation() got = %v, want %v", got, want)
	}
	runVariationWithCheck(
		t,
		"Float64Variation",
		(*GoFeatureFlag).Float64Variation,
		false,
		false,
		assertFloat64,
		[]variationCase[float64, float64]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119.12)),
							fixtures.VariationFalse:   new(any(121.12)),
							fixtures.VariationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        120.12,
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="120.12", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(randomKeySsssOne),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.AnonymousEqTrue),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 90,
									fixtures.VariationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119.12)),
							fixtures.VariationFalse:   new(any(121.12)),
							fixtures.VariationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        121.12,
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="121.12", variation="False"`,
			},
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationFalse:   new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationTrue:    new(any(fixtures.PlaceholderXxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        118.12,
				wantErr:     true,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="118.12", variation="SdkDefault"`,
			},
			{
				name: noExportedLog,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119.12)),
							fixtures.VariationFalse:   new(any(121.12)),
							fixtures.VariationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
						TrackEvents: new(false),
					}, nil),
				},
				want:        120.12,
				wantErr:     false,
				expectedLog: "",
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want:        118.12,
				wantErr:     false,
				expectedLog: "",
			},
		})
}

func TestFloat64VariationDetails(t *testing.T) {
	t.Parallel()

	type args = variationArgs[float64]

	runVariationTests(
		t,
		"Float64VariationDetails",
		(*GoFeatureFlag).Float64VariationDetails,
		[]variationCase[float64, model.VariationResult[float64]]{
			{
				name: callVariationBeforeInit,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  123.3,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VarTrue:  new(any(true)),
							fixtures.VarFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarFalse),
						},
					}, nil),
					disableInit: true,
				},
				wantErr: true,
			},
			{
				name: getDefaultValueIf,
				args: args{
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  120.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         120.12,
					TrackEvents:   true,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonDisabled,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="disable-flag", value="120.12", variation="SdkDefault"`,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				wantErr:     true,
				expectedLog: logKeyNotExistFloat,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				wantErr:     true,
				expectedLog: logKeyNotExistFloat,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119.12)),
							fixtures.VariationFalse:   new(any(121.12)),
							fixtures.VariationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         119.12,
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Reason:        flag.ReasonDefault,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="119.12", variation="Default"`,
			},
		})
}

func TestFloat64VariationDetails_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[float64]

	runVariationTests(
		t,
		"Float64VariationDetails",
		(*GoFeatureFlag).Float64VariationDetails,
		[]variationCase[float64, model.VariationResult[float64]]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119.12)),
							fixtures.VariationFalse:   new(any(121.12)),
							fixtures.VariationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         120.12,
					TrackEvents:   true,
					VariationType: fixtures.VariationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="120.12", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(randomKeySsssOne),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.AnonymousEqTrue),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 90,
									fixtures.VariationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119.12)),
							fixtures.VariationFalse:   new(any(121.12)),
							fixtures.VariationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         121.12,
					TrackEvents:   true,
					VariationType: fixtures.VariationFalse,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatchSplit,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="121.12", variation="False"`,
			},
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationFalse:   new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationTrue:    new(any(fixtures.PlaceholderXxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         118.12,
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Reason:        flag.ReasonDefault,
				},
				wantErr:     true,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="118.12", variation="SdkDefault"`,
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         118.12,
					TrackEvents:   false,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonOffline,
				},
				wantErr:     false,
				expectedLog: "",
			},
		})
}

func TestJSONArrayVariation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[[]any]

	assertJSONArray := func(t *testing.T, want, got []any) {
		t.Helper()
		assert.Equal(t, want, got, jsonArrayVariationGotV, got, want)
	}
	runVariationWithCheck(
		t,
		jsonArrayVariation,
		(*GoFeatureFlag).JSONArrayVariation,
		true,
		false,
		assertJSONArray,
		[]variationCase[[]any, []any]{
			{
				name: callVariationBeforeInit,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VarTrue:  new(any(true)),
							fixtures.VarFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarFalse),
						},
					}, nil),
					disableInit: true,
				},
				want:    []any{},
				wantErr: true,
			},
			{
				name: getDefaultValueIf,
				args: args{
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want:        []any{fixtures.PlaceholderToto},
				wantErr:     false,
				expectedLog: `user="random-key", flag="disable-flag", value="[toto]", variation="SdkDefault"`,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				want:    []any{fixtures.PlaceholderToto},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testhelpers.RFC3339Regex + userRandomKeyFlag +
					toLogSuffix,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				want:    []any{fixtures.PlaceholderToto},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testhelpers.RFC3339Regex + userRandomKeyFlag +
					toLogSuffix,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any([]any{fixtures.FlagsetDefault})),
							fixtures.VariationTrue:    new(any([]any{fixtures.TrueLiteral})),
							fixtures.VariationFalse:   new(any([]any{fixtures.FalseLiteral})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        []any{fixtures.FlagsetDefault},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="[default]", variation="Default"`,
			},
		})
}

func TestJSONArrayVariation_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[[]any]

	assertJSONArray := func(t *testing.T, want, got []any) {
		t.Helper()
		assert.Equal(t, want, got, jsonArrayVariationGotV, got, want)
	}
	runVariationWithCheck(
		t,
		jsonArrayVariation,
		(*GoFeatureFlag).JSONArrayVariation,
		true,
		false,
		assertJSONArray,
		[]variationCase[[]any, []any]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any([]any{fixtures.FlagsetDefault})),
							fixtures.VariationTrue:    new(any([]any{fixtures.TrueLiteral})),
							fixtures.VariationFalse:   new(any([]any{fixtures.FalseLiteral})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        []any{fixtures.TrueLiteral},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="[true]", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(randomKeySsssOne),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any([]any{fixtures.FlagsetDefault})),
							fixtures.VariationTrue:    new(any([]any{fixtures.TrueLiteral})),
							fixtures.VariationFalse:   new(any([]any{fixtures.FalseLiteral})),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleDefault),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 90,
								fixtures.VariationTrue:  10,
							},
						},
					}, nil),
				},
				want:        []any{fixtures.FalseLiteral},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="[false]", variation="False"`,
			},
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationFalse:   new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationTrue:    new(any(fixtures.PlaceholderXxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:    []any{fixtures.PlaceholderToto},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testhelpers.RFC3339Regex +
					"\\] user=\"random-key-ssss1\", flag=\"test-flag\", value=\"\\[" +
					"toto\\]\"\n",
			},
			{
				name: noExportedLog,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any([]any{fixtures.FlagsetDefault})),
							fixtures.VariationTrue:    new(any([]any{fixtures.TrueLiteral})),
							fixtures.VariationFalse:   new(any([]any{fixtures.FalseLiteral})),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleDefault),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
						TrackEvents: new(false),
					}, nil),
				},
				want:        []any{fixtures.TrueLiteral},
				wantErr:     false,
				expectedLog: "",
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want:        []any{fixtures.PlaceholderToto},
				wantErr:     false,
				expectedLog: "",
			},
		})
}

func TestJSONArrayVariationDetails(t *testing.T) {
	t.Parallel()

	type args = variationArgs[[]any]

	assertJSONArrayDetails := func(t *testing.T, want, got model.VariationResult[[]any]) {
		t.Helper()
		assert.Equal(t, want, got, jsonArrayVariationGotV, got, want)
	}
	runVariationWithCheck(
		t,
		jsonArrayVariation,
		(*GoFeatureFlag).JSONArrayVariationDetails,
		true,
		false,
		assertJSONArrayDetails,
		[]variationCase[[]any, model.VariationResult[[]any]]{
			{
				name: callVariationBeforeInit,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VarTrue:  new(any(true)),
							fixtures.VarFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarFalse),
						},
					}, nil),
					disableInit: true,
				},
				wantErr: true,
			},
			{
				name: getDefaultValueIf,
				args: args{
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonDisabled,
					Value:         []any{fixtures.PlaceholderToto},
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="disable-flag", value="[toto]", variation="SdkDefault"`,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testhelpers.RFC3339Regex + userRandomKeyFlag +
					toLogSuffix,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testhelpers.RFC3339Regex + userRandomKeyFlag +
					toLogSuffix,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any([]any{fixtures.FlagsetDefault})),
							fixtures.VariationTrue:    new(any([]any{fixtures.TrueLiteral})),
							fixtures.VariationFalse:   new(any([]any{fixtures.FalseLiteral})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Reason:        flag.ReasonDefault,
					Value:         []any{fixtures.FlagsetDefault},
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="[default]", variation="Default"`,
			},
		})
}

func TestJSONArrayVariationDetails_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[[]any]

	assertJSONArrayDetails := func(t *testing.T, want, got model.VariationResult[[]any]) {
		t.Helper()
		assert.Equal(t, want, got, jsonArrayVariationGotV, got, want)
	}
	runVariationWithCheck(
		t,
		jsonArrayVariation,
		(*GoFeatureFlag).JSONArrayVariationDetails,
		true,
		false,
		assertJSONArrayDetails,
		[]variationCase[[]any, model.VariationResult[[]any]]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any([]any{fixtures.FlagsetDefault})),
							fixtures.VariationTrue:    new(any([]any{fixtures.TrueLiteral})),
							fixtures.VariationFalse:   new(any([]any{fixtures.FalseLiteral})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: fixtures.VariationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         []any{fixtures.TrueLiteral},
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="[true]", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(randomKeySsssOne),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any([]any{fixtures.FlagsetDefault})),
							fixtures.VariationTrue:    new(any([]any{fixtures.TrueLiteral})),
							fixtures.VariationFalse:   new(any([]any{fixtures.FalseLiteral})),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleDefault),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 90,
								fixtures.VariationTrue:  10,
							},
						},
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: fixtures.VariationFalse,
					Failed:        false,
					Reason:        flag.ReasonSplit,
					Value:         []any{fixtures.FalseLiteral},
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="[false]", variation="False"`,
			},
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationFalse:   new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationTrue:    new(any(fixtures.PlaceholderXxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Reason:        flag.ReasonDefault,
					Value:         []any{fixtures.PlaceholderToto},
				},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testhelpers.RFC3339Regex +
					"\\] user=\"random-key-ssss1\", flag=\"test-flag\", value=\"\\[" +
					"toto\\]\"\n",
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  []any{fixtures.PlaceholderToto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   false,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonOffline,
					Value:         []any{fixtures.PlaceholderToto},
				},
				wantErr:     false,
				expectedLog: "",
			},
		})
}

func TestJSONVariation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[map[string]any]

	runVariationTests(
		t,
		"JSONVariation",
		(*GoFeatureFlag).JSONVariation,
		[]variationCase[map[string]any, map[string]any]{
			{
				name: callVariationBeforeInit,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  map[string]any{},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VarTrue:  new(any(true)),
							fixtures.VarFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarFalse),
						},
					}, nil),
					disableInit: true,
				},
				want:    map[string]any{},
				wantErr: true,
			},
			{
				name: getDefaultValueIf,
				args: args{
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want:        map[string]any{defaultNotkey: true},
				wantErr:     false,
				expectedLog: `user="random-key", flag="disable-flag", value="map[default-notkey:true]", variation="SdkDefault"`,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				want:        map[string]any{defaultNotkey: true},
				wantErr:     true,
				expectedLog: `user="random-key", flag="key-not-exist", value="map[default-notkey:true]", variation="SdkDefault"`,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				want:        map[string]any{defaultNotkey: true},
				wantErr:     true,
				expectedLog: `user="random-key", flag="key-not-exist", value="map[default-notkey:true]", variation="SdkDefault"`,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(map[string]any{fixtures.FlagsetDefault: true})),
							fixtures.VariationTrue:    new(any(map[string]any{fixtures.TrueLiteral: true})),
							fixtures.VariationFalse:   new(any(map[string]any{fixtures.FalseLiteral: true})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        map[string]any{fixtures.FlagsetDefault: true},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="map[default:true]", variation="Default"`,
			},
		})
}

func TestJSONVariation_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[map[string]any]

	runVariationTests(
		t,
		"JSONVariation",
		(*GoFeatureFlag).JSONVariation,
		[]variationCase[map[string]any, map[string]any]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(map[string]any{fixtures.FlagsetDefault: true})),
							fixtures.VariationTrue:    new(any(map[string]any{fixtures.TrueLiteral: true})),
							fixtures.VariationFalse:   new(any(map[string]any{fixtures.FalseLiteral: true})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        map[string]any{fixtures.TrueLiteral: true},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="map[true:true]", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(randomKeySsssOne),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.AnonymousEqTrue),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 90,
									fixtures.VariationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(map[string]any{fixtures.FlagsetDefault: true})),
							fixtures.VariationTrue:    new(any(map[string]any{fixtures.TrueLiteral: true})),
							fixtures.VariationFalse:   new(any(map[string]any{fixtures.FalseLiteral: true})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        map[string]any{fixtures.FalseLiteral: true},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="map[false:true]", variation="False"`,
			},
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationFalse:   new(any(fixtures.PlaceholderXxx)),
							fixtures.VariationTrue:    new(any(fixtures.PlaceholderXxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want:        map[string]any{defaultNotkey: true},
				wantErr:     true,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="map[default-notkey:true]", variation="SdkDefault"`,
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want:        map[string]any{defaultNotkey: true},
				wantErr:     false,
				expectedLog: "",
			},
		})
}

func TestJSONVariationDetails(t *testing.T) {
	t.Parallel()

	type args struct {
		evaluationCtx ffcontext.Context
		cacheMock     cache.Manager
		defaultValue  map[string]any
		flagKey       string
		offline       bool
		disableInit   bool
	}

	tests := []struct {
		name        string
		expectedLog string
		want        model.VariationResult[map[string]any]
		args        args
		wantErr     bool
	}{
		{
			name: getDefaultValueIf,
			args: args{
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  map[string]any{defaultNotkey: true},
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want: model.VariationResult[map[string]any]{
				TrackEvents:   true,
				VariationType: flag.VariationSDKDefault,
				Failed:        false,
				Reason:        flag.ReasonDisabled,
				Value:         map[string]any{defaultNotkey: true},
				Cacheable:     true,
			},
			wantErr:     false,
			expectedLog: `user="random-key", flag="disable-flag", value="map[default-notkey:true]", variation="SdkDefault"`,
		},
		{
			name: getDefaultValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  map[string]any{defaultNotkey: true},
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(map[string]any{fixtures.FlagsetDefault: true})),
						fixtures.VariationTrue:    new(any(map[string]any{fixtures.TrueLiteral: true})),
						fixtures.VariationFalse:   new(any(map[string]any{fixtures.FalseLiteral: true})),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[map[string]any]{
				TrackEvents:   true,
				VariationType: fixtures.VariationDefault,
				Failed:        false,
				Reason:        flag.ReasonDefault,
				Value:         map[string]any{fixtures.FlagsetDefault: true},
				Cacheable:     true,
			},
			wantErr:     false,
			expectedLog: `user="random-key", flag="test-flag", value="map[default:true]", variation="Default"`,
		},
		{
			name: getTrueValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
				defaultValue:  map[string]any{defaultNotkey: true},
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(map[string]any{fixtures.FlagsetDefault: true})),
						fixtures.VariationTrue:    new(any(map[string]any{fixtures.TrueLiteral: true})),
						fixtures.VariationFalse:   new(any(map[string]any{fixtures.FalseLiteral: true})),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[map[string]any]{
				TrackEvents:   true,
				VariationType: fixtures.VariationTrue,
				Failed:        false,
				Reason:        flag.ReasonTargetingMatch,
				Value:         map[string]any{fixtures.TrueLiteral: true},
				Cacheable:     true,
				Metadata: map[string]any{
					fixtures.EvaluatedRuleName: fixtures.RuleOne,
				},
			},
			wantErr:     false,
			expectedLog: `user="random-key", flag="test-flag", value="map[true:true]", variation="True"`,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(randomKeySsssOne),
				defaultValue:  map[string]any{defaultNotkey: true},
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.AnonymousEqTrue),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 90,
								fixtures.VariationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(map[string]any{fixtures.FlagsetDefault: true})),
						fixtures.VariationTrue:    new(any(map[string]any{fixtures.TrueLiteral: true})),
						fixtures.VariationFalse:   new(any(map[string]any{fixtures.FalseLiteral: true})),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[map[string]any]{
				TrackEvents:   true,
				VariationType: fixtures.VariationFalse,
				Failed:        false,
				Reason:        flag.ReasonTargetingMatchSplit,
				Value:         map[string]any{fixtures.FalseLiteral: true},
				Cacheable:     true,
				Metadata: map[string]any{
					fixtures.EvaluatedRuleName: fixtures.RuleOne,
				},
			},
			wantErr:     false,
			expectedLog: `user="random-key-ssss1", flag="test-flag", value="map[false:true]", variation="False"`,
		},
		{
			name: getSdkDefaultValue,
			args: args{
				offline:       true,
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  map[string]any{defaultNotkey: true},
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want: model.VariationResult[map[string]any]{
				TrackEvents:   false,
				VariationType: flag.VariationSDKDefault,
				Failed:        false,
				Reason:        flag.ReasonOffline,
				Value:         map[string]any{defaultNotkey: true},
			},
			wantErr:     false,
			expectedLog: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := slogassert.New(t, slog.LevelInfo, nil)
			logger := slog.New(handler)

			var client *GoFeatureFlag

			if !tt.args.disableInit {
				mgrCfg := retriever.ManagerConfig{
					FileFormat:              yaml,
					DisableNotifierOnInit:   false,
					StartWithRetrieverError: false,
					EnablePollingJitter:     true,
					PollingInterval:         500,
				}
				client = &GoFeatureFlag{
					evaluationService: flag.NewService(),
					retrieverManager: retriever.NewManager(
						mgrCfg,
						[]retriever.Retriever{},
						tt.args.cacheMock,
						logger,
					),
					config: Config{
						PollingInterval: 0,
						LeveledLogger:   logger,
						Offline:         tt.args.offline,
					},
					featureEventDataExporter: exporter.NewManager[exporter.FeatureEvent](
						[]exporter.Config{
							exporter.Config{
								FlushInterval:    0,
								MaxEventInMemory: 0,
								Exporter: &logsexporter.Exporter{
									LogFormat: userUserKeyFlagKey +
										valueValueVariationVariation,
								},
							}},
						exporter.DefaultExporterCleanQueueInterval,
						logger,
					),
				}
			}

			got, err := client.JSONVariationDetails(tt.args.flagKey, tt.args.evaluationCtx, tt.args.defaultValue)

			assertExpectedLog(t, handler, tt.expectedLog)

			if tt.wantErr {
				assert.Error(t, err, "JSONVariation() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			assert.Equal(t, tt.want, got, "JSONVariation() got = %v, want %v", got, tt.want)
		})
	}
}

func TestStringVariation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[string]

	runVariationTests(t, "StringVariation", (*GoFeatureFlag).StringVariation, []variationCase[string, string]{
		{
			name: callVariationBeforeInit,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  "",
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Variations: &map[string]*any{
						fixtures.VarTrue:  new(any(true)),
						fixtures.VarFalse: new(any(false)),
					},
					Rules: &[]flag.Rule{{
						Query:       new(fixtures.QueryKeyEqKey),
						Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
					},
					BucketingKey: nil,
					DefaultRule: &flag.Rule{
						VariationResult: new(fixtures.VarFalse),
					},
				}, nil),
				disableInit: true,
			},
			want:    "",
			wantErr: true,
		},
		{
			name: getDefaultValueIf,
			args: args{
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want:        defaultNotkey,
			wantErr:     false,
			expectedLog: `user="random-key", flag="disable-flag", value="default-notkey", variation="SdkDefault"`,
		},
		{
			name: getErrorWhenCache,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(
					&flag.InternalFlag{},
					errImpossibleToReadThe),
			},
			want:        defaultNotkey,
			wantErr:     true,
			expectedLog: `user="random-key", flag="key-not-exist", value="default-notkey", variation="SdkDefault"`,
		},
		{
			name: getDefaultValueWith,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(
					&flag.InternalFlag{},
					errFlagKeyNotExist,
				),
			},
			want:        defaultNotkey,
			wantErr:     true,
			expectedLog: `user="random-key", flag="key-not-exist", value="default-notkey", variation="SdkDefault"`,
		},

		{
			name: getDefaultValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        fixtures.FlagsetDefault,
			wantErr:     false,
			expectedLog: `user="random-key", flag="test-flag", value="default", variation="Default"`,
		},
	})
}

func TestStringVariation_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[string]

	runVariationTests(t, "StringVariation", (*GoFeatureFlag).StringVariation, []variationCase[string, string]{
		{
			name: getTrueValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        fixtures.TrueLiteral,
			wantErr:     false,
			expectedLog: logTestFlagBool,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(randomKeySsssOne),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.AnonymousEqTrue),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 90,
								fixtures.VariationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        fixtures.FalseLiteral,
			wantErr:     false,
			expectedLog: userRandomKeySsss,
		},
		{
			name: getDefaultValueWhen,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(1)),
						fixtures.VariationFalse:   new(any(2)),
						fixtures.VariationTrue:    new(any(3)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        defaultNotkey,
			wantErr:     true,
			expectedLog: `user="random-key-ssss1", flag="test-flag", value="default-notkey", variation="SdkDefault"`,
		},
		{
			name: getSdkDefaultValue,
			args: args{
				offline:       true,
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want:        defaultNotkey,
			wantErr:     false,
			expectedLog: "",
		},
		{
			name: "Semver prerelease comparison - numeric prerelease identifiers",
			args: args{
				flagKey: fixtures.FlagKeyTest,
				evaluationCtx: ffcontext.NewEvaluationContextBuilder("user-key").
					AddCustom("version", "1.0.0-2345").
					Build(),
				defaultValue: fixtures.FlagsetDefault,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:            new("semver_rule"),
							Query:           new("version gt \"1.0.0-1234\""),
							VariationResult: new("prerelease"),
						},
					},
					Variations: &map[string]*any{
						"prerelease":            new(any("newer-prerelease")),
						fixtures.FlagsetDefault: new(any("default-value")),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.FlagsetDefault),
					},
				}, nil),
			},
			want:        "newer-prerelease",
			wantErr:     false,
			expectedLog: `user="user-key", flag="test-flag", value="newer-prerelease", variation="prerelease"`,
		},
	})
}

func TestStringVariationDetails(t *testing.T) {
	t.Parallel()

	type args struct {
		evaluationCtx ffcontext.Context
		cacheMock     cache.Manager
		flagKey       string
		defaultValue  string
		offline       bool
		disableInit   bool
	}

	tests := []struct {
		name        string
		expectedLog string
		want        model.VariationResult[string]
		args        args
		wantErr     bool
	}{
		{
			name: getDefaultValueIf,
			args: args{
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want: model.VariationResult[string]{
				TrackEvents:   true,
				VariationType: flag.VariationSDKDefault,
				Failed:        false,
				Reason:        flag.ReasonDisabled,
				Value:         defaultNotkey,
				Cacheable:     true,
			},
			wantErr:     false,
			expectedLog: `user="random-key", flag="disable-flag", value="default-notkey", variation="SdkDefault"`,
		},
		{
			name: getDefaultValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[string]{
				TrackEvents:   true,
				VariationType: fixtures.VariationDefault,
				Failed:        false,
				Reason:        flag.ReasonDefault,
				Value:         fixtures.FlagsetDefault,
				Cacheable:     true,
			},
			wantErr:     false,
			expectedLog: `user="random-key", flag="test-flag", value="default", variation="Default"`,
		},
		{
			name: getTrueValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[string]{
				TrackEvents:   true,
				VariationType: fixtures.VariationTrue,
				Failed:        false,
				Reason:        flag.ReasonTargetingMatch,
				Value:         fixtures.TrueLiteral,
				Cacheable:     true,
				Metadata: map[string]any{
					fixtures.EvaluatedRuleName: fixtures.RuleOne,
				},
			},
			wantErr:     false,
			expectedLog: logTestFlagBool,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(randomKeySsssOne),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.AnonymousEqTrue),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 90,
								fixtures.VariationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[string]{
				TrackEvents:   true,
				VariationType: fixtures.VariationFalse,
				Failed:        false,
				Reason:        flag.ReasonTargetingMatchSplit,
				Value:         fixtures.FalseLiteral,
				Cacheable:     true,
				Metadata: map[string]any{
					fixtures.EvaluatedRuleName: fixtures.RuleOne,
				},
			},
			wantErr:     false,
			expectedLog: userRandomKeySsss,
		},
		{
			name: getSdkDefaultValue,
			args: args{
				offline:       true,
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want: model.VariationResult[string]{
				TrackEvents:   false,
				VariationType: flag.VariationSDKDefault,
				Failed:        false,
				Reason:        flag.ReasonOffline,
				Value:         defaultNotkey,
			},
			wantErr:     false,
			expectedLog: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := slogassert.New(t, slog.LevelInfo, nil)
			logger := slog.New(handler)

			var client *GoFeatureFlag

			if !tt.args.disableInit {
				mgrCfg := retriever.ManagerConfig{
					FileFormat:              yaml,
					DisableNotifierOnInit:   false,
					StartWithRetrieverError: false,
					EnablePollingJitter:     true,
					PollingInterval:         500,
				}
				client = &GoFeatureFlag{
					evaluationService: flag.NewService(),
					retrieverManager: retriever.NewManager(
						mgrCfg,
						[]retriever.Retriever{},
						tt.args.cacheMock,
						logger,
					),
					config: Config{
						PollingInterval: 0,
						LeveledLogger:   logger,
						Offline:         tt.args.offline,
					},
					featureEventDataExporter: exporter.NewManager[exporter.FeatureEvent](
						[]exporter.Config{exporter.Config{
							FlushInterval:    0,
							MaxEventInMemory: 0,
							Exporter: &logsexporter.Exporter{
								LogFormat: userUserKeyFlagKey +
									valueValueVariationVariation,
							},
						}},
						exporter.DefaultExporterCleanQueueInterval,
						logger,
					),
				}
			}

			got, err := client.StringVariationDetails(tt.args.flagKey, tt.args.evaluationCtx, tt.args.defaultValue)

			assertExpectedLog(t, handler, tt.expectedLog)

			if tt.wantErr {
				assert.Error(t, err, "StringVariation() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			assert.Equal(t, tt.want, got, "StringVariation() got = %v, want %v", got, tt.want)
		})
	}
}

func TestIntVariation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[int]

	runVariationTests(t, intVariation, (*GoFeatureFlag).IntVariation, []variationCase[int, int]{
		{
			name: callVariationBeforeInit,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  1,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Variations: &map[string]*any{
						fixtures.VarTrue:  new(any(true)),
						fixtures.VarFalse: new(any(false)),
					},
					Rules: &[]flag.Rule{{
						Query:       new(fixtures.QueryKeyEqKey),
						Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
					},
					BucketingKey: nil,
					DefaultRule: &flag.Rule{
						VariationResult: new(fixtures.VarFalse),
					},
				}, nil),
				disableInit: true,
			},
			want:    1,
			wantErr: true,
		},
		{
			name: getDefaultValueIf,
			args: args{
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  125,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want:        125,
			wantErr:     false,
			expectedLog: `user="random-key", flag="disable-flag", value="125", variation="SdkDefault"`,
		},
		{
			name: getErrorWhenCache,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  118,
				cacheMock: NewCacheMock(
					&flag.InternalFlag{},
					errImpossibleToReadThe),
			},
			want:        118,
			wantErr:     true,
			expectedLog: `user="random-key", flag="key-not-exist", value="118", variation="SdkDefault"`,
		},
		{
			name: getDefaultValueWith,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  118,
				cacheMock: NewCacheMock(
					&flag.InternalFlag{},
					errFlagKeyNotExist,
				),
			},
			want:        118,
			wantErr:     true,
			expectedLog: `user="random-key", flag="key-not-exist", value="118", variation="SdkDefault"`,
		},
		{
			name: "Get default value rule not apply",
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(119)),
						fixtures.VariationTrue:    new(any(120)),
						fixtures.VariationFalse:   new(any(121)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        119,
			wantErr:     false,
			expectedLog: `user="random-key", flag="test-flag", value="119", variation="Default"`,
		},
	})
}

func TestIntVariation_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[int]

	runVariationTests(t, intVariation, (*GoFeatureFlag).IntVariation, []variationCase[int, int]{
		{
			name: getTrueValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(119)),
						fixtures.VariationTrue:    new(any(120)),
						fixtures.VariationFalse:   new(any(121)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        120,
			wantErr:     false,
			expectedLog: logTestFlagInt,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(randomKeySsssOne),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.AnonymousEqTrue),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 90,
								fixtures.VariationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(119)),
						fixtures.VariationTrue:    new(any(120)),
						fixtures.VariationFalse:   new(any(121)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        121,
			wantErr:     false,
			expectedLog: `user="random-key-ssss1", flag="test-flag", value="121", variation="False"`,
		},
		{
			name: getDefaultValueWhen,
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsssOne),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.PlaceholderXxx)),
						fixtures.VariationFalse:   new(any(fixtures.PlaceholderXxx)),
						fixtures.VariationTrue:    new(any(fixtures.PlaceholderXxx)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        118,
			wantErr:     true,
			expectedLog: `user="random-key-ssss1", flag="test-flag", value="118", variation="SdkDefault"`,
		},
		{
			name: "Convert float to Int",
			args: args{
				flagKey:       fixtures.FlagKeyTest,
				evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(fixtures.RuleOne),
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(119.1)),
						fixtures.VariationTrue:    new(any(120.1)),
						fixtures.VariationFalse:   new(any(121.1)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(fixtures.RuleDefault),
						VariationResult: new(fixtures.VariationDefault),
					},
				}, nil),
			},
			want:        120,
			wantErr:     false,
			expectedLog: logTestFlagInt,
		},
		{
			name: getSdkDefaultValue,
			args: args{
				offline:       true,
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
				defaultValue:  125,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want:        125,
			wantErr:     false,
			expectedLog: "",
		},
	})
}

func TestIntVariationDetails(t *testing.T) {
	t.Parallel()

	type args = variationArgs[int]

	runVariationTests(
		t,
		intVariation,
		(*GoFeatureFlag).IntVariationDetails,
		[]variationCase[int, model.VariationResult[int]]{
			{
				name: getDefaultValueIf,
				args: args{
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  125,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   true,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonDisabled,
					Value:         125,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="disable-flag", value="125", variation="SdkDefault"`,
			},
			{
				name: "Get default value rule not apply",
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  118,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119)),
							fixtures.VariationTrue:    new(any(120)),
							fixtures.VariationFalse:   new(any(121)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Reason:        flag.ReasonDefault,
					Value:         119,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="119", variation="Default"`,
			},
		})
}

func TestIntVariationDetails_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[int]

	runVariationTests(
		t,
		intVariation,
		(*GoFeatureFlag).IntVariationDetails,
		[]variationCase[int, model.VariationResult[int]]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  118,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119)),
							fixtures.VariationTrue:    new(any(120)),
							fixtures.VariationFalse:   new(any(121)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   true,
					VariationType: fixtures.VariationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         120,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: logTestFlagInt,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(randomKeySsssOne),
					defaultValue:  118,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.AnonymousEqTrue),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 90,
									fixtures.VariationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119)),
							fixtures.VariationTrue:    new(any(120)),
							fixtures.VariationFalse:   new(any(121)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   true,
					VariationType: fixtures.VariationFalse,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatchSplit,
					Value:         121,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="121", variation="False"`,
			},
			{
				name: "Convert float to Int",
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  118,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(119.1)),
							fixtures.VariationTrue:    new(any(120.1)),
							fixtures.VariationFalse:   new(any(121.1)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   true,
					VariationType: fixtures.VariationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         120,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: logTestFlagInt,
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  125,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   false,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonOffline,
					Value:         125,
				},
				wantErr:     false,
				expectedLog: "",
			},
		})
}

func TestRawVariation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[any]

	assertRaw := func(t *testing.T, want, got model.RawVarResult) {
		t.Helper()
		assert.Equal(t, want, got, "RawVariation() got = %v, want %v", got, want)
	}
	runVariationWithCheck(
		t,
		"RawVariation",
		(*GoFeatureFlag).RawVariation,
		false,
		true,
		assertRaw,
		[]variationCase[any, model.RawVarResult]{
			{
				name: callVariationBeforeInit,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  "",
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VarTrue:  new(any(true)),
							fixtures.VarFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(fixtures.QueryKeyEqKey),
							Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarFalse),
						},
					}, nil),
					disableInit: true,
				},
				wantErr: true,
				want: model.RawVarResult{
					VariationType: flag.VariationSDKDefault,
					Failed:        true,
					Reason:        flag.ReasonError,
					ErrorCode:     flag.ErrorCodeProviderNotReady,
					Value:         "",
				},
			},
			{
				name: getDefaultValueIf,
				args: args{
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.RawVarResult{
					Value:         true,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					TrackEvents:   true,
					Reason:        flag.ReasonDisabled,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: logDisableFlagBool,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  "defaultValue",
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				want: model.RawVarResult{
					Value:         "defaultValue",
					VariationType: flag.VariationSDKDefault,
					Failed:        true,
					TrackEvents:   true,
					Reason:        flag.ReasonError,
					ErrorCode:     flag.ErrorCodeFlagNotFound,
					Cacheable:     false,
				},
				wantErr:     true,
				expectedLog: `user="random-key", flag="key-not-exist", value="defaultValue", variation="SdkDefault"`,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  123456,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				want: model.RawVarResult{
					Value:         123456,
					VariationType: flag.VariationSDKDefault,
					Failed:        true,
					TrackEvents:   true,
					Reason:        flag.ReasonError,
					ErrorCode:     flag.ErrorCodeFlagNotFound,
				},
				wantErr:     true,
				expectedLog: `user="random-key", flag="key-not-exist", value="123456", variation="SdkDefault"`,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  map[string]any{test123: fixtures.PlaceholderTest},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(
								any(map[string]any{fixtures.PlaceholderTest: fixtures.PlaceholderTest}),
							),
							fixtures.VariationTrue:  new(any(map[string]any{testTwo: fixtures.PlaceholderTest})),
							fixtures.VariationFalse: new(any(map[string]any{testThree: fixtures.PlaceholderTest})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.RawVarResult{
					Value:         map[string]any{fixtures.PlaceholderTest: fixtures.PlaceholderTest},
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					TrackEvents:   true,
					Reason:        flag.ReasonDefault,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="map[test:test]", variation="Default"`,
			},
		})
}

func TestRawVariation_RuleEvaluation(t *testing.T) {
	t.Parallel()

	type args = variationArgs[any]

	assertRaw := func(t *testing.T, want, got model.RawVarResult) {
		t.Helper()
		assert.Equal(t, want, got, "RawVariation() got = %v, want %v", got, want)
	}
	runVariationWithCheck(
		t,
		"RawVariation",
		(*GoFeatureFlag).RawVariation,
		false,
		true,
		assertRaw,
		[]variationCase[any, model.RawVarResult]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  map[string]any{test123: fixtures.PlaceholderTest},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(
								any(map[string]any{fixtures.PlaceholderTest: fixtures.PlaceholderTest}),
							),
							fixtures.VariationTrue:  new(any(map[string]any{testTwo: fixtures.PlaceholderTest})),
							fixtures.VariationFalse: new(any(map[string]any{testThree: fixtures.PlaceholderTest})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.RawVarResult{
					Value:         map[string]any{testTwo: fixtures.PlaceholderTest},
					VariationType: fixtures.VariationTrue,
					Failed:        false,
					TrackEvents:   true,
					Reason:        flag.ReasonTargetingMatch,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="map[test2:test]", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(randomKeySsssOne),
					defaultValue:  map[string]any{test123: fixtures.PlaceholderTest},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.AnonymousEqTrue),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 90,
									fixtures.VariationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(
								any(map[string]any{fixtures.PlaceholderTest: fixtures.PlaceholderTest}),
							),
							fixtures.VariationTrue:  new(any(map[string]any{testTwo: fixtures.PlaceholderTest})),
							fixtures.VariationFalse: new(any(map[string]any{testThree: fixtures.PlaceholderTest})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					}, nil),
				},
				want: model.RawVarResult{
					Value:         map[string]any{testThree: fixtures.PlaceholderTest},
					VariationType: fixtures.VariationFalse,
					Failed:        false,
					TrackEvents:   true,
					Reason:        flag.ReasonTargetingMatchSplit,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="map[test3:test]", variation="False"`,
			},
			{
				name: noExportedLog,
				args: args{
					flagKey:       fixtures.FlagKeyTest,
					evaluationCtx: anonymousContext(fixtures.UserKeyRandom),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
							fixtures.VariationFalse:   new(any(false)),
						},
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
						TrackEvents: new(false),
					}, nil),
				},
				want: model.RawVarResult{
					Value:         true,
					VariationType: fixtures.VariationTrue,
					Failed:        false,
					TrackEvents:   false,
					Reason:        flag.ReasonTargetingMatch,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.EvaluatedRuleName: fixtures.RuleOne,
					},
				},
				wantErr:     false,
				expectedLog: "",
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  false,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.RawVarResult{
					Value:         false,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					TrackEvents:   false,
					Reason:        flag.ReasonOffline,
				},
				wantErr:     false,
				expectedLog: "",
			},
			{
				name: "should use interface default value if the flag is disabled",
				args: args{
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(fixtures.UserKeyRandom),
					defaultValue:  nil,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.RawVarResult{
					TrackEvents:   true,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonDisabled,
					Value:         nil,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: "",
			},
		})
}

func Test_OverrideContextEnrichmentWithEnvironment(t *testing.T) {
	t.Parallel()

	tempFile, err := os.CreateTemp(t.TempDir(), "")
	require.NoError(t, err)

	defer tempFile.Close()

	err = os.WriteFile(tempFile.Name(), []byte(`
flag1:
 variations:
   enabled: true
   disabled: false
 targeting:
   - query: env eq "staging"
     variation: enabled
 defaultRule:
   variation: disabled

`), 0o644)
	require.NoError(t, err)

	goff, err := New(&Config{
		PollingInterval: 500 * time.Millisecond,
		Retriever:       &fileretriever.Retriever{Path: tempFile.Name()},
		EvaluationContextEnrichment: map[string]any{
			env: staging,
		},
	})
	require.NoError(t, err)

	res, err1 := goff.BoolVariation(
		fixtures.FlagKeyOne,
		ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyMy).Build(),
		false,
	)
	assert.True(t, res)
	require.NoError(t, err1)

	allFlags := goff.AllFlagsState(ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyMy).Build())
	assert.Equal(t, true, allFlags.GetFlags()[fixtures.FlagKeyOne].Value)

	goff2, err2 := New(&Config{
		PollingInterval: 500 * time.Millisecond,
		Retriever:       &fileretriever.Retriever{Path: tempFile.Name()},
		Environment:     staging,
		EvaluationContextEnrichment: map[string]any{
			env: staging,
		},
	})
	require.NoError(t, err2)

	res2, err3 := goff2.BoolVariation(
		fixtures.FlagKeyOne,
		ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyMy).Build(),
		false,
	)
	assert.True(t, res2)
	require.NoError(t, err3)

	allFlags2 := goff2.AllFlagsState(ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyMy).Build())
	assert.Equal(t, true, allFlags2.GetFlags()[fixtures.FlagKeyOne].Value)

	// Explicit environment should override the environment from the enrichment
	goff3, err4 := New(&Config{
		PollingInterval: 500 * time.Millisecond,
		Retriever:       &fileretriever.Retriever{Path: tempFile.Name()},
		Environment:     staging,
		EvaluationContextEnrichment: map[string]any{
			env: "prod",
		},
	})
	require.NoError(t, err4)

	res3, err5 := goff3.BoolVariation(
		fixtures.FlagKeyOne,
		ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyMy).Build(),
		false,
	)
	assert.True(t, res3)
	require.NoError(t, err5)

	allFlags3 := goff3.AllFlagsState(ffcontext.NewEvaluationContextBuilder(fixtures.UserKeyMy).Build())
	assert.Equal(t, true, allFlags3.GetFlags()[fixtures.FlagKeyOne].Value)
}

// anonymousContext builds the evaluation context with the deprecated constructor, which the tests keep covering.
func anonymousContext(key string) ffcontext.EvaluationContext {
	//nolint:staticcheck // SA1019: the deprecated constructor is under test
	return ffcontext.NewAnonymousEvaluationContext(key)
}
