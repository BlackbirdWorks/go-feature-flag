package fixtures

// Fixture file paths and patterns shared by the tests of several packages.
const (
	// AYAML is the fixture file path or pattern "a.yaml".
	AYAML = "a.yaml"
	// AllDefaultJSONPath is the fixture file path or pattern "./testdata/all_default.json".
	AllDefaultJSONPath = "./testdata/all_default.json"
	// ConfigFlagsYAML is the fixture file path or pattern "config/flags.yaml".
	ConfigFlagsYAML = "config/flags.yaml"
	// FileAYAML is the fixture file path or pattern "f/a.yaml".
	FileAYAML = "f/a.yaml"
	// FileBYAML is the fixture file path or pattern "f/b.yaml".
	FileBYAML = "f/b.yaml"
	// FlagConfigFile is the fixture file path or pattern "flag-config.yaml".
	FlagConfigFile = "flag-config.yaml"
	// FlagConfigYAMLParentPath is the path of the flag configuration fixture from a package three levels deep.
	FlagConfigYAMLParentPath = "../../../testdata/flag-config.yaml"
	// FlagConfigYAMLPath is the path of the flag configuration fixture, relative to the package under test.
	FlagConfigYAMLPath = "testdata/flag-config.yaml"
	// FlagGoffYAMLPath is the fixture file path or pattern "testdata/flag.goff.yaml".
	FlagGoffYAMLPath = "testdata/flag.goff.yaml"
	// FlagVariationPrefixPattern is the fixture file path or pattern "^flag-variation-".
	FlagVariationPrefixPattern = "^flag-variation-"
	// FlagsAYAML is the fixture file path or pattern "flags/a.yaml".
	FlagsAYAML = "flags/a.yaml"
	// FlagsBYAML is the fixture file path or pattern "flags/b.yaml".
	FlagsBYAML = "flags/b.yaml"
	// FlagsConfigGoffYAML is the fixture file path or pattern "flags/config.goff.yaml".
	FlagsConfigGoffYAML = "flags/config.goff.yaml"
	// FlagsDir is the fixture file path or pattern "flags".
	FlagsDir = "flags"
	// FlagsDirSlash is the fixture file path or pattern "flags/".
	FlagsDirSlash = "flags/"
	// FlagsSubCYAML is the fixture file path or pattern "flags/sub/c.yaml".
	FlagsSubCYAML = "flags/sub/c.yaml"
	// JSONFileSuffixPattern is the fixture file path or pattern "-[0-9]*\\.json$".
	JSONFileSuffixPattern = "-[0-9]*\\.json$"
	// YAMLGlob is the fixture file path or pattern "*.yaml".
	YAMLGlob = "*.yaml"
)
