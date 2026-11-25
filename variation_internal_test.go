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
	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testutils"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	anonymousEqTrue              = "anonymous eq true"
	boolVariationDetails         = "BoolVariationDetails"
	callVariationBeforeInit      = "Call variation before init of SDK"
	defaultNotkey                = "default-notkey"
	defaultRule                  = "defaultRule"
	disableFlag                  = "disable-flag"
	evaluatedRuleName2           = "evaluatedRuleName"
	variationDefault             = "Default"
	lowerDefault                 = "default"
	bracketPrefixPattern         = "^\\["
	variationFalse               = "False"
	lowerFalse                   = "false"
	variationTrue                = "True"
	lowerTrue                    = "true"
	flag12                       = "flag1"
	getDefaultValueIf            = "Get default value if flag disable"
	getDefaultValueRule          = "Get default value, rule not apply"
	getDefaultValueWhen          = "Get default value, when rule apply and not right type"
	getDefaultValueWith          = "Get default value with key not exist"
	getErrorWhenCache            = "Get error when cache not init"
	getFalseValueRule            = "Get false value, rule apply"
	getSdkDefaultValue           = "Get sdk default value if offline"
	getTrueValueRule             = "Get true value, rule apply"
	intVariation                 = "IntVariation"
	jSONArrayVariation           = "JSONArrayVariation"
	jSONArrayVariationGotVWant   = "JSONArrayVariation() got = %v, want %v"
	keyEqKey                     = "key eq \"key\""
	keyEqRandomKey               = "key eq \"random-key\""
	keyNotExist                  = "key-not-exist"
	myKey                        = "my-key"
	noExportedLog                = "No exported log"
	off2                         = "off"
	randomKey                    = "random-key"
	randomKeySsss1               = "random-key-ssss1"
	rule1                        = "rule1"
	staging                      = "staging"
	test                         = "test"
	test123                      = "test123"
	test2                        = "test2"
	test3                        = "test3"
	testFlag                     = "test-flag"
	to                           = "to\\]\"\n"
	toto                         = "toto"
	userRandomKeyFlag            = "\\] user=\"random-key\", flag=\"key-not-exist\", value=\"\\[to"
	userRandomKeyFlag2           = `user="random-key", flag="disable-flag", value="true", variation="SdkDefault"`
	userRandomKeyFlag3           = `user="random-key", flag="key-not-exist", value="118.12", variation="SdkDefault"`
	userRandomKeyFlag4           = `user="random-key", flag="key-not-exist", value="true", variation="SdkDefault"`
	userRandomKeyFlag5           = `user="random-key", flag="test-flag", value="120", variation="True"`
	userRandomKeyFlag6           = `user="random-key", flag="test-flag", value="true", variation="True"`
	userRandomKeySsss            = `user="random-key-ssss1", flag="test-flag", value="false", variation="False"`
	userUserKeyFlagKey           = "user=\"{{ .UserKey}}\", flag=\"{{ .Key}}\", "
	valueValueVariationVariation = "value=\"{{ .Value}}\", variation=\"{{ .Variation}}\""
	varFalse                     = "var_false"
	varTrue                      = "var_true"
	xxx                          = "xxx"
	yAML                         = "YAML"
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

func (c *cacheMock) GetLatestUpdateDate() time.Time {
	return clocktest.Instant()
}

func (c *cacheMock) ConvertToFlagStruct(_ []byte, _ string) (map[string]dto.DTO, error) {
	return nil, assert.AnError
}
func (c *cacheMock) UpdateCache(_ map[string]dto.DTO, _ *fflog.FFLogger, _ bool) error {
	return nil
}

func (c *cacheMock) Close() {}
func (c *cacheMock) GetFlag(_ string) (flag.Flag, error) {
	return c.flag, c.err
}

func (c *cacheMock) AllFlags() (map[string]flag.Flag, error) { return nil, assert.AnError }

func (c *cacheMock) AllFlagsReadOnly() (map[string]flag.Flag, error) { return nil, assert.AnError }

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
			return strings.Contains(message.Message, expectedLog)
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
		FileFormat:              yAML,
		DisableNotifierOnInit:   false,
		StartWithRetrieverError: false,
		EnablePollingJitter:     true,
		PollingInterval:         500,
	}
	ffLogger := &fflog.FFLogger{LeveledLogger: logger}

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

			if tt.wantErr {
				if alwaysAssertResult {
					require.Error(t, err, "%s() error = %v, wantErr %v", label, err, tt.wantErr)
				} else {
					assert.Error(t, err, "%s() error = %v, wantErr %v", label, err, tt.wantErr)

					return
				}
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  false,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Variations: &map[string]*any{
						varTrue:  new(any(true)),
						varFalse: new(any(false)),
					},
					Rules: &[]flag.Rule{{
						Query:       new(keyEqKey),
						Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
					},
					BucketingKey: nil,
					DefaultRule: &flag.Rule{
						VariationResult: new(varFalse),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Disable: new(true),
				}, nil),
			},
			want:        true,
			wantErr:     false,
			expectedLog: userRandomKeyFlag2,
		},
		{
			name: getErrorWhenCache,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  true,
				cacheMock: NewCacheMock(
					&flag.InternalFlag{},
					errImpossibleToReadThe),
			},
			want:        true,
			wantErr:     true,
			expectedLog: userRandomKeyFlag4,
		},
		{
			name: getDefaultValueWith,
			args: args{
				flagKey:       keyNotExist,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  true,
				cacheMock: NewCacheMock(
					&flag.InternalFlag{},
					errFlagKeyNotExist,
				),
			},
			want:        true,
			wantErr:     true,
			expectedLog: userRandomKeyFlag4,
		},
		{
			name: getDefaultValueRule,
			args: args{
				flagKey:       testFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(true)),
						variationFalse:   new(any(false)),
						variationTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
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
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKey),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqRandomKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(false)),
						variationFalse:   new(any(false)),
						variationTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want:        true,
			wantErr:     false,
			expectedLog: userRandomKeyFlag6,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKeySsss1),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(anonymousEqTrue),
							Percentages: &map[string]float64{
								variationFalse: 90,
								variationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(false)),
						variationFalse:   new(any(false)),
						variationTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
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
				flagKey:       testFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqRandomKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(xxx)),
						variationFalse:   new(any(xxx)),
						variationTrue:    new(any(xxx)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
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
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKey),
				defaultValue:  true,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqRandomKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(false)),
						variationFalse:   new(any(false)),
						variationTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  false,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							varTrue:  new(any(true)),
							varFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(keyEqKey),
							Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(varFalse),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				expectedLog: userRandomKeyFlag2,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  true,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				wantErr:     true,
				expectedLog: userRandomKeyFlag4,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  true,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				wantErr:     true,
				expectedLog: userRandomKeyFlag4,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(true)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: variationDefault,
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
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: variationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         true,
					TrackEvents:   true,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: userRandomKeyFlag6,
			},
			{
				name: "Get rule name on metadata, rule apply",
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: variationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         true,
					TrackEvents:   true,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: userRandomKeyFlag6,
			},
			{
				name: "Get no rule name on metadata, rule apply has not name",
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: variationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         true,
					TrackEvents:   true,
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: userRandomKeyFlag6,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKeySsss1),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(anonymousEqTrue),
								Percentages: &map[string]float64{
									variationFalse: 90,
									variationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[bool]{
					VariationType: variationFalse,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatchSplit,
					Value:         false,
					TrackEvents:   true,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
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
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(xxx)),
							variationFalse:   new(any(xxx)),
							variationTrue:    new(any(xxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(""),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							"on": new(any(true)),
							off2: new(any(false)),
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
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(""),
					defaultValue:  false,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							"on": new(any(true)),
							off2: new(any(false)),
						},
						DefaultRule: &flag.Rule{
							Percentages: &map[string]float64{
								"on": 100,
								off2: 0,
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  123.3,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							varTrue:  new(any(true)),
							varFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(keyEqKey),
							Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(varFalse),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				want:        118.12,
				wantErr:     true,
				expectedLog: userRandomKeyFlag3,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				want:        118.12,
				wantErr:     true,
				expectedLog: userRandomKeyFlag3,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119.12)),
							variationFalse:   new(any(121.12)),
							variationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
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
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119.12)),
							variationFalse:   new(any(121.12)),
							variationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
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
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKeySsss1),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(anonymousEqTrue),
								Percentages: &map[string]float64{
									variationFalse: 90,
									variationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119.12)),
							variationFalse:   new(any(121.12)),
							variationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
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
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(xxx)),
							variationFalse:   new(any(xxx)),
							variationTrue:    new(any(xxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
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
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119.12)),
							variationFalse:   new(any(121.12)),
							variationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  123.3,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							varTrue:  new(any(true)),
							varFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(keyEqKey),
							Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(varFalse),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				wantErr:     true,
				expectedLog: userRandomKeyFlag3,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				wantErr:     true,
				expectedLog: userRandomKeyFlag3,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119.12)),
							variationFalse:   new(any(121.12)),
							variationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         119.12,
					TrackEvents:   true,
					VariationType: variationDefault,
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
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119.12)),
							variationFalse:   new(any(121.12)),
							variationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         120.12,
					TrackEvents:   true,
					VariationType: variationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="120.12", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKeySsss1),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(anonymousEqTrue),
								Percentages: &map[string]float64{
									variationFalse: 90,
									variationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119.12)),
							variationFalse:   new(any(121.12)),
							variationTrue:    new(any(120.12)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         121.12,
					TrackEvents:   true,
					VariationType: variationFalse,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatchSplit,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="121.12", variation="False"`,
			},
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
					defaultValue:  118.12,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(xxx)),
							variationFalse:   new(any(xxx)),
							variationTrue:    new(any(xxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[float64]{
					Value:         118.12,
					TrackEvents:   true,
					VariationType: variationDefault,
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
		assert.Equal(t, want, got, jSONArrayVariationGotVWant, got, want)
	}
	runVariationWithCheck(
		t,
		jSONArrayVariation,
		(*GoFeatureFlag).JSONArrayVariation,
		true,
		false,
		assertJSONArray,
		[]variationCase[[]any, []any]{
			{
				name: callVariationBeforeInit,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							varTrue:  new(any(true)),
							varFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(keyEqKey),
							Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(varFalse),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want:        []any{toto},
				wantErr:     false,
				expectedLog: `user="random-key", flag="disable-flag", value="[toto]", variation="SdkDefault"`,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				want:    []any{toto},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testutils.RFC3339Regex + userRandomKeyFlag +
					to,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				want:    []any{toto},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testutils.RFC3339Regex + userRandomKeyFlag +
					to,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any([]any{lowerDefault})),
							variationTrue:    new(any([]any{lowerTrue})),
							variationFalse:   new(any([]any{lowerFalse})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want:        []any{lowerDefault},
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
		assert.Equal(t, want, got, jSONArrayVariationGotVWant, got, want)
	}
	runVariationWithCheck(
		t,
		jSONArrayVariation,
		(*GoFeatureFlag).JSONArrayVariation,
		true,
		false,
		assertJSONArray,
		[]variationCase[[]any, []any]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any([]any{lowerDefault})),
							variationTrue:    new(any([]any{lowerTrue})),
							variationFalse:   new(any([]any{lowerFalse})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want:        []any{lowerTrue},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="[true]", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKeySsss1),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any([]any{lowerDefault})),
							variationTrue:    new(any([]any{lowerTrue})),
							variationFalse:   new(any([]any{lowerFalse})),
						},
						DefaultRule: &flag.Rule{
							Name: new(defaultRule),
							Percentages: &map[string]float64{
								variationFalse: 90,
								variationTrue:  10,
							},
						},
					}, nil),
				},
				want:        []any{lowerFalse},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="[false]", variation="False"`,
			},
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(xxx)),
							variationFalse:   new(any(xxx)),
							variationTrue:    new(any(xxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want:    []any{toto},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testutils.RFC3339Regex + "\\] user=\"random-key-ssss1\", flag=\"test-flag\", value=\"\\[" +
					"toto\\]\"\n",
			},
			{
				name: noExportedLog,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any([]any{lowerDefault})),
							variationTrue:    new(any([]any{lowerTrue})),
							variationFalse:   new(any([]any{lowerFalse})),
						},
						DefaultRule: &flag.Rule{
							Name: new(defaultRule),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
						TrackEvents: new(false),
					}, nil),
				},
				want:        []any{lowerTrue},
				wantErr:     false,
				expectedLog: "",
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want:        []any{toto},
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
		assert.Equal(t, want, got, jSONArrayVariationGotVWant, got, want)
	}
	runVariationWithCheck(
		t,
		jSONArrayVariation,
		(*GoFeatureFlag).JSONArrayVariationDetails,
		true,
		false,
		assertJSONArrayDetails,
		[]variationCase[[]any, model.VariationResult[[]any]]{
			{
				name: callVariationBeforeInit,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							varTrue:  new(any(true)),
							varFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(keyEqKey),
							Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(varFalse),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonDisabled,
					Value:         []any{toto},
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="disable-flag", value="[toto]", variation="SdkDefault"`,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errImpossibleToReadThe),
				},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testutils.RFC3339Regex + userRandomKeyFlag +
					to,
			},
			{
				name: getDefaultValueWith,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(
						&flag.InternalFlag{},
						errFlagKeyNotExist,
					),
				},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testutils.RFC3339Regex + userRandomKeyFlag +
					to,
			},
			{
				name: getDefaultValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any([]any{lowerDefault})),
							variationTrue:    new(any([]any{lowerTrue})),
							variationFalse:   new(any([]any{lowerFalse})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: variationDefault,
					Failed:        false,
					Reason:        flag.ReasonDefault,
					Value:         []any{lowerDefault},
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
		assert.Equal(t, want, got, jSONArrayVariationGotVWant, got, want)
	}
	runVariationWithCheck(
		t,
		jSONArrayVariation,
		(*GoFeatureFlag).JSONArrayVariationDetails,
		true,
		false,
		assertJSONArrayDetails,
		[]variationCase[[]any, model.VariationResult[[]any]]{
			{
				name: getTrueValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any([]any{lowerDefault})),
							variationTrue:    new(any([]any{lowerTrue})),
							variationFalse:   new(any([]any{lowerFalse})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: variationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         []any{lowerTrue},
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="[true]", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKeySsss1),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any([]any{lowerDefault})),
							variationTrue:    new(any([]any{lowerTrue})),
							variationFalse:   new(any([]any{lowerFalse})),
						},
						DefaultRule: &flag.Rule{
							Name: new(defaultRule),
							Percentages: &map[string]float64{
								variationFalse: 90,
								variationTrue:  10,
							},
						},
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: variationFalse,
					Failed:        false,
					Reason:        flag.ReasonSplit,
					Value:         []any{lowerFalse},
					Cacheable:     true,
				},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="[false]", variation="False"`,
			},
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(xxx)),
							variationFalse:   new(any(xxx)),
							variationTrue:    new(any(xxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   true,
					VariationType: variationDefault,
					Failed:        false,
					Reason:        flag.ReasonDefault,
					Value:         []any{toto},
				},
				wantErr: true,
				expectedLog: bracketPrefixPattern + testutils.RFC3339Regex + "\\] user=\"random-key-ssss1\", flag=\"test-flag\", value=\"\\[" +
					"toto\\]\"\n",
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  []any{toto},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Disable: new(true),
					}, nil),
				},
				want: model.VariationResult[[]any]{
					TrackEvents:   false,
					VariationType: flag.VariationSDKDefault,
					Failed:        false,
					Reason:        flag.ReasonOffline,
					Value:         []any{toto},
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  map[string]any{},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							varTrue:  new(any(true)),
							varFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(keyEqKey),
							Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(varFalse),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(map[string]any{lowerDefault: true})),
							variationTrue:    new(any(map[string]any{lowerTrue: true})),
							variationFalse:   new(any(map[string]any{lowerFalse: true})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want:        map[string]any{lowerDefault: true},
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
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(map[string]any{lowerDefault: true})),
							variationTrue:    new(any(map[string]any{lowerTrue: true})),
							variationFalse:   new(any(map[string]any{lowerFalse: true})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want:        map[string]any{lowerTrue: true},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="map[true:true]", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKeySsss1),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(anonymousEqTrue),
								Percentages: &map[string]float64{
									variationFalse: 90,
									variationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(map[string]any{lowerDefault: true})),
							variationTrue:    new(any(map[string]any{lowerTrue: true})),
							variationFalse:   new(any(map[string]any{lowerFalse: true})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want:        map[string]any{lowerFalse: true},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="map[false:true]", variation="False"`,
			},
			{
				name: getDefaultValueWhen,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
					defaultValue:  map[string]any{defaultNotkey: true},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(xxx)),
							variationFalse:   new(any(xxx)),
							variationTrue:    new(any(xxx)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				flagKey:       testFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  map[string]any{defaultNotkey: true},
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(map[string]any{lowerDefault: true})),
						variationTrue:    new(any(map[string]any{lowerTrue: true})),
						variationFalse:   new(any(map[string]any{lowerFalse: true})),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[map[string]any]{
				TrackEvents:   true,
				VariationType: variationDefault,
				Failed:        false,
				Reason:        flag.ReasonDefault,
				Value:         map[string]any{lowerDefault: true},
				Cacheable:     true,
			},
			wantErr:     false,
			expectedLog: `user="random-key", flag="test-flag", value="map[default:true]", variation="Default"`,
		},
		{
			name: getTrueValueRule,
			args: args{
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKey),
				defaultValue:  map[string]any{defaultNotkey: true},
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqRandomKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(map[string]any{lowerDefault: true})),
						variationTrue:    new(any(map[string]any{lowerTrue: true})),
						variationFalse:   new(any(map[string]any{lowerFalse: true})),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[map[string]any]{
				TrackEvents:   true,
				VariationType: variationTrue,
				Failed:        false,
				Reason:        flag.ReasonTargetingMatch,
				Value:         map[string]any{lowerTrue: true},
				Cacheable:     true,
				Metadata: map[string]any{
					evaluatedRuleName2: rule1,
				},
			},
			wantErr:     false,
			expectedLog: `user="random-key", flag="test-flag", value="map[true:true]", variation="True"`,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKeySsss1),
				defaultValue:  map[string]any{defaultNotkey: true},
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(anonymousEqTrue),
							Percentages: &map[string]float64{
								variationFalse: 90,
								variationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(map[string]any{lowerDefault: true})),
						variationTrue:    new(any(map[string]any{lowerTrue: true})),
						variationFalse:   new(any(map[string]any{lowerFalse: true})),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[map[string]any]{
				TrackEvents:   true,
				VariationType: variationFalse,
				Failed:        false,
				Reason:        flag.ReasonTargetingMatchSplit,
				Value:         map[string]any{lowerFalse: true},
				Cacheable:     true,
				Metadata: map[string]any{
					evaluatedRuleName2: rule1,
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					FileFormat:              yAML,
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
						&fflog.FFLogger{LeveledLogger: logger},
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
						&fflog.FFLogger{LeveledLogger: logger},
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  "",
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Variations: &map[string]*any{
						varTrue:  new(any(true)),
						varFalse: new(any(false)),
					},
					Rules: &[]flag.Rule{{
						Query:       new(keyEqKey),
						Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
					},
					BucketingKey: nil,
					DefaultRule: &flag.Rule{
						VariationResult: new(varFalse),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				flagKey:       testFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(lowerDefault)),
						variationTrue:    new(any(lowerTrue)),
						variationFalse:   new(any(lowerFalse)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want:        lowerDefault,
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
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKey),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqRandomKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(lowerDefault)),
						variationTrue:    new(any(lowerTrue)),
						variationFalse:   new(any(lowerFalse)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want:        lowerTrue,
			wantErr:     false,
			expectedLog: userRandomKeyFlag6,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKeySsss1),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(anonymousEqTrue),
							Percentages: &map[string]float64{
								variationFalse: 90,
								variationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(lowerDefault)),
						variationTrue:    new(any(lowerTrue)),
						variationFalse:   new(any(lowerFalse)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want:        lowerFalse,
			wantErr:     false,
			expectedLog: userRandomKeySsss,
		},
		{
			name: getDefaultValueWhen,
			args: args{
				flagKey:       testFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(1)),
						variationFalse:   new(any(2)),
						variationTrue:    new(any(3)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				flagKey: testFlag,
				evaluationCtx: ffcontext.NewEvaluationContextBuilder("user-key").
					AddCustom("version", "1.0.0-2345").
					Build(),
				defaultValue: lowerDefault,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:            new("semver_rule"),
							Query:           new("version gt \"1.0.0-1234\""),
							VariationResult: new("prerelease"),
						},
					},
					Variations: &map[string]*any{
						"prerelease": new(any("newer-prerelease")),
						lowerDefault: new(any("default-value")),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(lowerDefault),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				flagKey:       testFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(lowerDefault)),
						variationTrue:    new(any(lowerTrue)),
						variationFalse:   new(any(lowerFalse)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[string]{
				TrackEvents:   true,
				VariationType: variationDefault,
				Failed:        false,
				Reason:        flag.ReasonDefault,
				Value:         lowerDefault,
				Cacheable:     true,
			},
			wantErr:     false,
			expectedLog: `user="random-key", flag="test-flag", value="default", variation="Default"`,
		},
		{
			name: getTrueValueRule,
			args: args{
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKey),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqRandomKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(lowerDefault)),
						variationTrue:    new(any(lowerTrue)),
						variationFalse:   new(any(lowerFalse)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[string]{
				TrackEvents:   true,
				VariationType: variationTrue,
				Failed:        false,
				Reason:        flag.ReasonTargetingMatch,
				Value:         lowerTrue,
				Cacheable:     true,
				Metadata: map[string]any{
					evaluatedRuleName2: rule1,
				},
			},
			wantErr:     false,
			expectedLog: userRandomKeyFlag6,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKeySsss1),
				defaultValue:  defaultNotkey,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(anonymousEqTrue),
							Percentages: &map[string]float64{
								variationFalse: 90,
								variationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(lowerDefault)),
						variationTrue:    new(any(lowerTrue)),
						variationFalse:   new(any(lowerFalse)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want: model.VariationResult[string]{
				TrackEvents:   true,
				VariationType: variationFalse,
				Failed:        false,
				Reason:        flag.ReasonTargetingMatchSplit,
				Value:         lowerFalse,
				Cacheable:     true,
				Metadata: map[string]any{
					evaluatedRuleName2: rule1,
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					FileFormat:              yAML,
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
						&fflog.FFLogger{LeveledLogger: logger},
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
						&fflog.FFLogger{LeveledLogger: logger},
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  1,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Variations: &map[string]*any{
						varTrue:  new(any(true)),
						varFalse: new(any(false)),
					},
					Rules: &[]flag.Rule{{
						Query:       new(keyEqKey),
						Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
					},
					BucketingKey: nil,
					DefaultRule: &flag.Rule{
						VariationResult: new(varFalse),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				flagKey:       testFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(119)),
						variationTrue:    new(any(120)),
						variationFalse:   new(any(121)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
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
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKey),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqRandomKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(119)),
						variationTrue:    new(any(120)),
						variationFalse:   new(any(121)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want:        120,
			wantErr:     false,
			expectedLog: userRandomKeyFlag5,
		},
		{
			name: getFalseValueRule,
			args: args{
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKeySsss1),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(anonymousEqTrue),
							Percentages: &map[string]float64{
								variationFalse: 90,
								variationTrue:  10,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(119)),
						variationTrue:    new(any(120)),
						variationFalse:   new(any(121)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
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
				flagKey:       testFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKeySsss1),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(xxx)),
						variationFalse:   new(any(xxx)),
						variationTrue:    new(any(xxx)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
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
				flagKey:       testFlag,
				evaluationCtx: anonymousContext(randomKey),
				defaultValue:  118,
				cacheMock: NewCacheMock(&flag.InternalFlag{
					Rules: &[]flag.Rule{
						{
							Name:  new(rule1),
							Query: new(keyEqRandomKey),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					Variations: &map[string]*any{
						variationDefault: new(any(119.1)),
						variationTrue:    new(any(120.1)),
						variationFalse:   new(any(121.1)),
					},
					DefaultRule: &flag.Rule{
						Name:            new(defaultRule),
						VariationResult: new(variationDefault),
					},
				}, nil),
			},
			want:        120,
			wantErr:     false,
			expectedLog: userRandomKeyFlag5,
		},
		{
			name: getSdkDefaultValue,
			args: args{
				offline:       true,
				flagKey:       disableFlag,
				evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  118,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119)),
							variationTrue:    new(any(120)),
							variationFalse:   new(any(121)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   true,
					VariationType: variationDefault,
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
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  118,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119)),
							variationTrue:    new(any(120)),
							variationFalse:   new(any(121)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   true,
					VariationType: variationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         120,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: userRandomKeyFlag5,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKeySsss1),
					defaultValue:  118,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(anonymousEqTrue),
								Percentages: &map[string]float64{
									variationFalse: 90,
									variationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119)),
							variationTrue:    new(any(120)),
							variationFalse:   new(any(121)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   true,
					VariationType: variationFalse,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatchSplit,
					Value:         121,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="121", variation="False"`,
			},
			{
				name: "Convert float to Int",
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  118,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(119.1)),
							variationTrue:    new(any(120.1)),
							variationFalse:   new(any(121.1)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.VariationResult[int]{
					TrackEvents:   true,
					VariationType: variationTrue,
					Failed:        false,
					Reason:        flag.ReasonTargetingMatch,
					Value:         120,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: userRandomKeyFlag5,
			},
			{
				name: getSdkDefaultValue,
				args: args{
					offline:       true,
					flagKey:       disableFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  "",
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							varTrue:  new(any(true)),
							varFalse: new(any(false)),
						},
						Rules: &[]flag.Rule{{
							Query:       new(keyEqKey),
							Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
						},
						BucketingKey: nil,
						DefaultRule: &flag.Rule{
							VariationResult: new(varFalse),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
				expectedLog: userRandomKeyFlag2,
			},
			{
				name: getErrorWhenCache,
				args: args{
					flagKey:       keyNotExist,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					flagKey:       testFlag,
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
					defaultValue:  map[string]any{test123: test},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(map[string]any{test: test})),
							variationTrue:    new(any(map[string]any{test2: test})),
							variationFalse:   new(any(map[string]any{test3: test})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.RawVarResult{
					Value:         map[string]any{test: test},
					VariationType: variationDefault,
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
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  map[string]any{test123: test},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(map[string]any{test: test})),
							variationTrue:    new(any(map[string]any{test2: test})),
							variationFalse:   new(any(map[string]any{test3: test})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.RawVarResult{
					Value:         map[string]any{test2: test},
					VariationType: variationTrue,
					Failed:        false,
					TrackEvents:   true,
					Reason:        flag.ReasonTargetingMatch,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key", flag="test-flag", value="map[test2:test]", variation="True"`,
			},
			{
				name: getFalseValueRule,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKeySsss1),
					defaultValue:  map[string]any{test123: test},
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(anonymousEqTrue),
								Percentages: &map[string]float64{
									variationFalse: 90,
									variationTrue:  10,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(map[string]any{test: test})),
							variationTrue:    new(any(map[string]any{test2: test})),
							variationFalse:   new(any(map[string]any{test3: test})),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					}, nil),
				},
				want: model.RawVarResult{
					Value:         map[string]any{test3: test},
					VariationType: variationFalse,
					Failed:        false,
					TrackEvents:   true,
					Reason:        flag.ReasonTargetingMatchSplit,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
					},
				},
				wantErr:     false,
				expectedLog: `user="random-key-ssss1", flag="test-flag", value="map[test3:test]", variation="False"`,
			},
			{
				name: noExportedLog,
				args: args{
					flagKey:       testFlag,
					evaluationCtx: anonymousContext(randomKey),
					defaultValue:  true,
					cacheMock: NewCacheMock(&flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationTrue:    new(any(true)),
							variationFalse:   new(any(false)),
						},
						Rules: &[]flag.Rule{
							{
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
						TrackEvents: new(false),
					}, nil),
				},
				want: model.RawVarResult{
					Value:         true,
					VariationType: variationTrue,
					Failed:        false,
					TrackEvents:   false,
					Reason:        flag.ReasonTargetingMatch,
					Cacheable:     true,
					Metadata: map[string]any{
						evaluatedRuleName2: rule1,
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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
					evaluationCtx: ffcontext.NewEvaluationContext(randomKey),
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

	goff, err := New(Config{
		PollingInterval: 500 * time.Millisecond,
		Retriever:       &fileretriever.Retriever{Path: tempFile.Name()},
		EvaluationContextEnrichment: map[string]any{
			env2: staging,
		},
	})
	require.NoError(t, err)

	res, err1 := goff.BoolVariation(
		flag12,
		ffcontext.NewEvaluationContextBuilder(myKey).Build(),
		false,
	)
	assert.True(t, res)
	require.NoError(t, err1)

	allFlags := goff.AllFlagsState(ffcontext.NewEvaluationContextBuilder(myKey).Build())
	assert.Equal(t, true, allFlags.GetFlags()[flag12].Value)

	goff2, err2 := New(Config{
		PollingInterval: 500 * time.Millisecond,
		Retriever:       &fileretriever.Retriever{Path: tempFile.Name()},
		Environment:     staging,
		EvaluationContextEnrichment: map[string]any{
			env2: staging,
		},
	})
	require.NoError(t, err2)

	res2, err3 := goff2.BoolVariation(
		flag12,
		ffcontext.NewEvaluationContextBuilder(myKey).Build(),
		false,
	)
	assert.True(t, res2)
	require.NoError(t, err3)

	allFlags2 := goff2.AllFlagsState(ffcontext.NewEvaluationContextBuilder(myKey).Build())
	assert.Equal(t, true, allFlags2.GetFlags()[flag12].Value)

	// Explicit environment should override the environment from the enrichment
	goff3, err4 := New(Config{
		PollingInterval: 500 * time.Millisecond,
		Retriever:       &fileretriever.Retriever{Path: tempFile.Name()},
		Environment:     staging,
		EvaluationContextEnrichment: map[string]any{
			env2: "prod",
		},
	})
	require.NoError(t, err4)

	res3, err5 := goff3.BoolVariation(
		flag12,
		ffcontext.NewEvaluationContextBuilder(myKey).Build(),
		false,
	)
	assert.True(t, res3)
	require.NoError(t, err5)

	allFlags3 := goff3.AllFlagsState(ffcontext.NewEvaluationContextBuilder(myKey).Build())
	assert.Equal(t, true, allFlags3.GetFlags()[flag12].Value)
}

// anonymousContext builds the evaluation context with the deprecated constructor, which the tests keep covering.
func anonymousContext(key string) ffcontext.EvaluationContext {
	//nolint:staticcheck // SA1019: the deprecated constructor is under test
	return ffcontext.NewAnonymousEvaluationContext(key)
}
