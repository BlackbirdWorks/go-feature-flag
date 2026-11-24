package linter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLinter_Lint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		linter   Linter
		wantErrs []string
	}{
		{
			name: "flag-config.yaml",
			linter: Linter{
				InputFile:   "../../../testdata/flag-config.yaml",
				InputFormat: "YAML",
			},
			wantErrs: []string{},
		},
		{
			name: "flag-config.json",
			linter: Linter{
				InputFile:   "../../../testdata/flag-config.json",
				InputFormat: "JSON",
			},
			wantErrs: []string{},
		},
		{
			name: "flag-config.toml",
			linter: Linter{
				InputFile:   "../../../testdata/flag-config.toml",
				InputFormat: "TOML",
			},
			wantErrs: []string{},
		},
		{
			name: "invalid yaml",
			linter: Linter{
				InputFile:   "testdata/invalid.yaml",
				InputFormat: "yaml",
			},
			wantErrs: []string{
				"testdata/invalid.yaml: could not parse file (yaml): yaml: unmarshal errors:\n  line 7: cannot unmarshal !!st" +
					"r \u0060toto\u0060 into map[string]float64",
			},
		},
		{
			name: "invalid json",
			linter: Linter{
				InputFile:   "testdata/invalid.json",
				InputFormat: "json",
			},
			wantErrs: []string{
				"testdata/invalid.json: could not parse file (json): invalid character ':' after top-level value",
			},
		},
		{
			name: "invalid input format",
			linter: Linter{
				InputFile:   "testdata/invalid.json",
				InputFormat: "swift",
			},
			wantErrs: []string{
				"testdata/invalid.json: could not parse file (yaml): yaml: line 16: did not find expected key",
			},
		},
		{
			name: "invalid toml",
			linter: Linter{
				InputFile:   "testdata/invalid.toml",
				InputFormat: "toml",
			},
			wantErrs: []string{
				"testdata/invalid.toml: could not parse file (toml): toml: basic strings cannot have new lines",
			},
		},
		{
			name: "no variation",
			linter: Linter{
				InputFile:   "testdata/no-variation.yaml",
				InputFormat: "yaml",
			},
			wantErrs: []string{"testdata/no-variation.yaml: invalid flag flag: no variation available"},
		},
		{
			name: "no default rule",
			linter: Linter{
				InputFile:   "testdata/no-default-rule.yaml",
				InputFormat: "yaml",
			},
			wantErrs: []string{"testdata/no-default-rule.yaml: invalid flag flag: missing default rule"},
		},
		{
			name: "invalid rule",
			linter: Linter{
				InputFile:   "testdata/invalid-rule.yaml",
				InputFormat: "yaml",
			},
			wantErrs: []string{"testdata/invalid-rule.yaml: invalid flag flag: each targeting should have a query"},
		},
		{
			name: "invalid file",
			linter: Linter{
				InputFile:   "testdata/unknown.yaml",
				InputFormat: "yaml",
			},
			wantErrs: []string{"impossible to find config file testdata/unknown.yaml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotErrs := make([]string, 0)
			for _, err := range tt.linter.Lint() {
				gotErrs = append(gotErrs, err.Error())
			}

			assert.Equal(t, tt.wantErrs, gotErrs)
		})
	}
}
