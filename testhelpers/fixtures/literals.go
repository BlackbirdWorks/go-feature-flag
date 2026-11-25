package fixtures

// Common literals shared by the tests of several packages.
const (
	// UserKeyAbc is the common literal "abc".
	UserKeyAbc = "abc"
	// AddressOne is the common literal "addr1".
	AddressOne = "addr1"
	// AddressTwo is the common literal "addr2".
	AddressTwo = "addr2"
	// DatabaseAnalytics is the common literal "analytics".
	DatabaseAnalytics = "analytics"
	// Attr1 is the common literal "attr1".
	Attr1 = "attr1"
	// Attr2 is the common literal "attr2".
	Attr2 = "attr2"
	// BadContent is the common literal "bad".
	BadContent = "bad"
	// BitbucketAPIURL is the common literal "https://api.bitbucket.goff.org".
	BitbucketAPIURL = "https://api.bitbucket.goff.org"
	// BoolType is the common literal "bool".
	BoolType = "bool"
	// TrackingEventKey is the common literal "checkout".
	TrackingEventKey = "checkout"
	// ConfigFlagName is the common literal "config".
	ConfigFlagName = "config"
	// CurrentDateTime is the common literal "currentDateTime".
	CurrentDateTime = "currentDateTime"
	// LogLevelDebug is the common literal "debug".
	LogLevelDebug = "debug"
	// DescriptionField is the common literal "description".
	DescriptionField = "description"
	// UserKeyEFGH is the common literal "EFGH".
	UserKeyEFGH = "EFGH"
	// AttrEnv is the common literal "env".
	AttrEnv = "env"
	// LogLevelError is the common literal "error".
	LogLevelError = "error"
	// EvaluatedRuleName is the common literal "evaluatedRuleName".
	EvaluatedRuleName = "evaluatedRuleName"
	// FalseLiteral is the common literal "false".
	FalseLiteral = "false"
	// KindFeature is the common literal "feature".
	KindFeature = "feature"
	// KindFile is the common literal "file".
	KindFile = "file"
	// FlagCEmptyYAML is the common literal "flagC: {}\n".
	FlagCEmptyYAML = "flagC: {}\n"
	// PlanFree is the common literal "free".
	PlanFree = "free"
	// KindGitHub is the common literal "github".
	KindGitHub = "github"
	// GoFeatureFlagOrg is the common literal "gofeatureflag".
	GoFeatureFlagOrg = "gofeatureflag"
	// GoogleStorage is the common literal "googleStorage".
	GoogleStorage = "googleStorage"
	// AttrGroup is the common literal "group".
	AttrGroup = "group"
	// KindHTTP is the common literal "http".
	KindHTTP = "http"
	// BodyHello is the common literal "hello".
	BodyHello = "hello"
	// LogLevelInfo is the common literal "info".
	LogLevelInfo = "info"
	// IntType is the common literal "int".
	IntType = "int"
	// IssueLink is the common literal "issue-link".
	IssueLink = "issue-link"
	// MetadataKey is the common literal "key".
	MetadataKey = "key"
	// Legacy is the common literal "legacy".
	Legacy = "legacy"
	// MyCluster is the common literal "my-cluster".
	MyCluster = "my-cluster"
	// MyWorkgroup is the common literal "my-workgroup".
	MyWorkgroup = "my-workgroup"
	// NestedKey is the common literal "nested".
	NestedKey = "nested"
	// NullLiteral is the common literal "null".
	NullLiteral = "null"
	// OFREPEvaluateFlagsPath is the common literal "/ofrep/v1/evaluate/flags".
	OFREPEvaluateFlagsPath = "/ofrep/v1/evaluate/flags"
	// AttrPlan is the common literal "plan".
	AttrPlan = "plan"
	// PlanPro is the common literal "pro".
	PlanPro = "pro"
	// EnvProd is the common literal "prod".
	EnvProd = "prod"
	// RepoGoFeatureFlag is the common literal "thomaspoignant/go-feature-flag".
	RepoGoFeatureFlag = "thomaspoignant/go-feature-flag"
	// RepoGoFeatureFlagConfig is the common literal "gofeatureflag/config-repo".
	RepoGoFeatureFlagConfig = "gofeatureflag/config-repo"
	// RepoMyOrg is the common literal "myorg/myrepo".
	RepoMyOrg = "myorg/myrepo"
	// LogKeyRequest is the common literal "request".
	LogKeyRequest = "request"
	// SourceServer is the common literal "SERVER".
	SourceServer = "SERVER"
	// SimpleFeatureFlagDescription is the common literal "this is a simple feature flag".
	SimpleFeatureFlagDescription = "this is a simple feature flag"
	// StringType is the common literal "string".
	StringType = "string"
	// LogMessageSuccess is the common literal "Success".
	LogMessageSuccess = "Success"
	// PlaceholderTest is the common literal "test".
	PlaceholderTest = "test"
	// TestFlagTrueVarYAML is the common literal a long text.
	TestFlagTrueVarYAML = "test-flag:\n" +
		"  variations:\n" +
		"    true_var: true\n" +
		"    false_var: false\n" +
		"  targeting:\n" +
		"    - query: key eq \"random-key\"\n" +
		"      percentage:\n" +
		"        true_var: 0\n" +
		"        false_var: 100\n" +
		"  defaultRule:\n" +
		"    variation: false_var\t\n"
	// PlaceholderToto is the common literal "toto".
	PlaceholderToto = "toto"
	// KindTracking is the common literal "tracking".
	KindTracking = "tracking"
	// TrueLiteral is the common literal "true".
	TrueLiteral = "true"
	// UnspecifiedAddress is the common literal "0.0.0.0".
	UnspecifiedAddress = "0.0.0.0"
	// ContextKindUser is the common literal "user".
	ContextKindUser = "user"
	// MetadataValue is the common literal "value".
	MetadataValue = "value"
	// Numeric123 is the common literal "123".
	Numeric123 = "123"
	// KindWebhook is the common literal "webhook".
	KindWebhook = "webhook"
	// WebsocketFlagChangePath is the common literal "/ws/v1/flag/change".
	WebsocketFlagChangePath = "/ws/v1/flag/change"
	// PlaceholderXxx is the common literal "xxx".
	PlaceholderXxx = "xxx"
	// ValueYO2 is the common literal "YO2".
	ValueYO2 = "YO2"
	// PlaceholderZzz is the common literal "zzz".
	PlaceholderZzz = "zzz"
)
