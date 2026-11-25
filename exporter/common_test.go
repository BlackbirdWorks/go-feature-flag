package exporter_test

import (
	"fmt"
	"os"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/exporter"
)

const (
	filenameFormat2                    = "filenameFormat"
	flagVariation                      = "flag-variation"
	json2                              = "json"
	kindContextKindUserKeyCreationDate = "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};"
	randomName                         = "random-name"
	valueDefaultN                      = "{{ .Value}};{{ .Default}}\n"
)

func Test_ParseTemplate(t *testing.T) {
	t.Parallel()

	type args struct {
		template        string
		defaultTemplate string
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "Invalid template",
			args: args{
				template: kindContextKindUserKeyCreationDate +
					"{{ .Value}};{{ .Default}",
				defaultTemplate: kindContextKindUserKeyCreationDate +
					valueDefaultN,
			},
			wantErr: true,
		},
		{
			name: "Valid template",
			args: args{
				template: kindContextKindUserKeyCreationDate,
				defaultTemplate: kindContextKindUserKeyCreationDate +
					valueDefaultN,
			},
			wantErr: false,
		},
		{
			name: "empty template use default",
			args: args{
				template: "",
				defaultTemplate: kindContextKindUserKeyCreationDate +
					valueDefaultN,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defaultT, _ := template.New(randomName).Parse(tt.args.defaultTemplate)

			assert.NotPanics(
				t,
				func() { exporter.ParseTemplate(randomName, tt.args.template, tt.args.defaultTemplate) },
			)

			got := exporter.ParseTemplate(randomName, tt.args.template, tt.args.defaultTemplate)

			if tt.wantErr {
				assert.Equal(t, defaultT, got, "If template invalid we should use default template")

				return
			}

			assert.NotEqual(t, defaultT, got, "We should not have the same template")
		})
	}
}

func TestComputeFilename(t *testing.T) {
	t.Parallel()

	hostname, _ := os.Hostname()

	type args struct {
		template *template.Template
		format   string
	}

	tests := []struct {
		wantErr assert.ErrorAssertionFunc
		args    args
		name    string
		want    string
	}{
		{
			name: "Nothing to template",
			args: args{
				template: exporter.ParseTemplate(
					filenameFormat2,
					flagVariation,
					flagVariation,
				),
				format: json2,
			},
			want:    flagVariation,
			wantErr: assert.NoError,
		},
		{
			name: "With extension",
			args: args{
				template: exporter.ParseTemplate(
					filenameFormat2,
					"flag-variation.{{ .Format }}",
					flagVariation,
				),
				format: json2,
			},
			want:    "flag-variation.json",
			wantErr: assert.NoError,
		},
		{
			name: "Multiple templates",
			args: args{
				template: exporter.ParseTemplate(
					filenameFormat2,
					"flag-variation-{{ .Hostname}}.{{ .Format}}",
					flagVariation,
				),
				format: json2,
			},
			want:    "flag-variation-" + hostname + ".json",
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := exporter.ComputeFilename(tt.args.template, tt.args.format)
			if !tt.wantErr(
				t,
				err,
				fmt.Sprintf("ComputeFilename(%v, %v)", tt.args.template, tt.args.format),
			) {
				return
			}

			assert.Equalf(
				t,
				tt.want,
				got,
				"ComputeFilename(%v, %v)",
				tt.args.template,
				tt.args.format,
			)
		})
	}
}

func TestFormatEventInCSV(t *testing.T) {
	t.Parallel()

	type args struct {
		csvTemplate *template.Template
		event       exporter.FeatureEvent
	}

	tests := []struct {
		args    args
		wantErr assert.ErrorAssertionFunc
		name    string
		want    string
	}{
		{
			name: "valid",
			args: args{
				csvTemplate: exporter.ParseTemplate(
					"exporterExample",
					exporter.DefaultCsvTemplate,
					exporter.DefaultCsvTemplate,
				),
				event: exporter.FeatureEvent{
					Kind:         feature,
					ContextKind:  anonymousUser,
					UserKey:      aBCD,
					CreationDate: 1617970547,
					Key:          randomKey,
					Variation:    fixtureDefault,
					Value:        "YO",
					Default:      false,
					Source:       sERVER,
				},
			},
			want:    "feature;anonymousUser;ABCD;1617970547;random-key;Default;YO;false;SERVER\n",
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.args.event.FormatInCSV(tt.args.csvTemplate)
			if !tt.wantErr(
				t,
				err,
				fmt.Sprintf("FormatInCSV(%v, %v)", tt.args.csvTemplate, tt.args.event),
			) {
				return
			}

			assert.Equalf(
				t,
				tt.want,
				string(got),
				"FormatInCSV(%v, %v)",
				tt.args.csvTemplate,
				tt.args.event,
			)
		})
	}
}

func TestFormatEventInJSON(t *testing.T) {
	t.Parallel()

	type args struct {
		event exporter.FeatureEvent
	}

	tests := []struct {
		args    args
		wantErr assert.ErrorAssertionFunc
		name    string
		want    string
	}{
		{
			name: "valid",
			args: args{event: exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    fixtureDefault,
				Value:        "YO",
				Default:      false,
				Source:       sERVER,
			}},
			want: "{\"kind\":\"feature\",\"contextKind\":\"anonymousUser\",\"userKey\":\"ABCD\",\"creationDate\":161797" +
				"0547,\"key\":\"random-key\",\"variation\":\"Default\",\"value\":\"YO\",\"default\":false,\"version\":\"\"," +
				"\"source\":\"SERVER\"}\n",
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.args.event.FormatInJSON()
			if !tt.wantErr(t, err, fmt.Sprintf("FormatInJSON(%v)", tt.args.event)) {
				return
			}

			assert.Equalf(t, tt.want, string(got), "FormatInJSON(%v)", tt.args.event)
		})
	}
}
