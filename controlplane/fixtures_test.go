package controlplane_test

import (
	"time"

	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	nameCheckoutFlags = "checkout-flags"
	nameFlagSets      = "flagsets"
	channelStable     = "stable"
	variationOff      = "off"
	queryBeta         = "beta eq true"
	queryXEq1         = "x eq 1"
	keyOwner          = "owner"
	textABC           = "abc"
	textYes           = "yes"
	textNull          = "null"
	algoMD5           = "md5"
	extJSON           = ".json"
	extYAML           = ".yaml"
	pathObject0Name   = "objects[0].name"
	connFlagsEU       = "flags-eu"
	connControl       = "control"
	apiKeyOne         = "6f1f1b7e-6a0e-4d4c-9d8e-2f6f3f1a0b11"
	textExp           = "1e3"
	textTrue          = "true"
	textDate          = "2026-01-01"
	keyFloat          = "float"
	resultOK          = "ok 2"
	reasonSyntax      = "syntax error"
	keyInt            = "int"
	keyHuge           = "huge"
	keyList           = "list"
)

// fixtureTime is a fixed instant: the tests never read the wall clock.
func fixtureTime(day int) *time.Time {
	t := time.Date(2026, time.January, day, 3, 4, 5, 0, time.UTC)

	return &t
}

// fixtureFlags covers every flag type; its values survive a JSON round trip (no integers in any values).
func fixtureFlags() controlplane.Flags {
	return controlplane.Flags{
		"new-checkout": {
			TrackEvents: new(false),
			Disable:     new(false),
			Version:     new("1.2"),
			Variations:  map[string]any{"on": true, variationOff: false},
			Targeting: []controlplane.Rule{
				{
					Name: new("beta"), Query: new(queryBeta), OnError: new("match"),
					Percentage: map[string]float64{"on": 100, variationOff: 0},
				},
				{
					Name:  new("ramp"),
					Query: new(`country eq "FR"`),
					ProgressiveRollout: &controlplane.ProgressiveRollout{
						Initial: &controlplane.ProgressiveRolloutStep{
							Variation:  new(variationOff),
							Percentage: new(0.0),
							Date:       fixtureTime(1),
						},
						End: &controlplane.ProgressiveRolloutStep{
							Variation:  new("on"),
							Percentage: new(100.0),
							Date:       fixtureTime(2),
						},
					},
				},
			},
			BucketingKey: new("targetingKey"),
			DefaultRule:  &controlplane.Rule{Variation: new(variationOff)},
			ScheduledRollout: []controlplane.ScheduledStep{{
				Date:        fixtureTime(3),
				DefaultRule: &controlplane.Rule{Variation: new("on")},
				Metadata:    map[string]any{"step": "full"},
			}},
			Experimentation: &controlplane.Experimentation{Start: fixtureTime(4), End: fixtureTime(5)},
			Metadata:        map[string]any{keyOwner: "payments", "ratio": 1.5, "tags": []any{textYes, "007"}},
		},
		"banner": {
			Variations:  map[string]any{"a": textYes, "b": "no", "c": 0.5, "d": textDate},
			DefaultRule: &controlplane.Rule{Variation: new("a")},
		},
	}
}

func fixtureManifest() *controlplane.Manifest {
	flags := controlplane.ChecksumOf([]byte("flags"))
	sets := controlplane.ChecksumOf([]byte("flag sets"))

	return &controlplane.Manifest{
		Channel:    channelStable,
		Generation: 7,
		CreatedAt:  fixtureTime(2),
		Objects: []controlplane.ManifestObject{
			{
				Name:       nameCheckoutFlags,
				Kind:       controlplane.KindFlags,
				Connection: connFlagsEU,
				Key:        keyOf(flags),
				Checksum:   flags,
				Size:       5,
				Format:     controlplane.FormatYAML,
				VersionID:  "v1",
			},
			{
				Name:       nameFlagSets,
				Kind:       controlplane.KindFlagSets,
				Connection: connControl,
				Version:    1,
				Key:        keyOf(sets),
				Checksum:   sets,
				Size:       9,
				Format:     controlplane.FormatYAML,
			},
		},
	}
}

func fixtureFlagSet() *controlplane.FlagSetDocument {
	return &controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{
		{
			Name:    "checkout",
			Sources: []string{nameCheckoutFlags},
			APIKeys: []string{apiKeyOne},
		},
		{Name: "no-keys-yet", Sources: []string{nameCheckoutFlags}},
	}}
}

// keyOf is the object key of a checksum; an error shows in the key, so a test that compares keys fails.
func keyOf(sum controlplane.Checksum) string {
	key, err := controlplane.ObjectKey(sum, controlplane.FormatYAML)
	if err != nil {
		return "error: " + err.Error()
	}

	return key
}
