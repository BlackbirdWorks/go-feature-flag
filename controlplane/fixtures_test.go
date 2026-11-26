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
	pathObject0Name   = "objects[0].name"
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
			Version:     "1.2",
			Variations:  map[string]any{"on": true, variationOff: false},
			Targeting: []controlplane.Rule{
				{Name: "beta", Query: queryBeta, Percentage: map[string]float64{"on": 100, variationOff: 0}},
				{
					Name:  "ramp",
					Query: `country eq "FR"`,
					ProgressiveRollout: &controlplane.ProgressiveRollout{
						Initial: &controlplane.ProgressiveRolloutStep{
							Variation:  variationOff,
							Percentage: new(0.0),
							Date:       fixtureTime(1),
						},
						End: &controlplane.ProgressiveRolloutStep{
							Variation:  "on",
							Percentage: new(100.0),
							Date:       fixtureTime(2),
						},
					},
				},
			},
			BucketingKey: "targetingKey",
			DefaultRule:  &controlplane.Rule{Variation: variationOff},
			ScheduledRollout: []controlplane.ScheduledStep{{
				Date:        fixtureTime(3),
				DefaultRule: &controlplane.Rule{Variation: "on"},
				Metadata:    map[string]any{"step": "full"},
			}},
			Experimentation: &controlplane.Experimentation{Start: fixtureTime(4), End: fixtureTime(5)},
			Metadata:        map[string]any{keyOwner: "payments", "ratio": 1.5, "tags": []any{"yes", "007"}},
		},
		"banner": {
			Variations:  map[string]any{"a": "yes", "b": "no", "c": 0.5, "d": "2026-01-01"},
			DefaultRule: &controlplane.Rule{Variation: "a"},
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
				Connection: "flags-eu",
				Key: controlplane.ObjectKey(
					flags,
					controlplane.FormatYAML,
				),
				Checksum:  flags,
				Size:      5,
				Format:    controlplane.FormatYAML,
				VersionID: "v1",
			},
			{
				Name:       nameFlagSets,
				Kind:       controlplane.KindFlagSets,
				Connection: "control",
				Version:    1,
				Key: controlplane.ObjectKey(
					sets,
					controlplane.FormatYAML,
				),
				Checksum: sets,
				Size:     9,
				Format:   controlplane.FormatYAML,
			},
		},
	}
}

func fixtureFlagSet() *controlplane.FlagSetDocument {
	return &controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{
		{
			Name:    "checkout",
			Sources: []string{nameCheckoutFlags},
			APIKeys: []string{"6f1f1b7e-6a0e-4d4c-9d8e-2f6f3f1a0b11"},
		},
		{Name: "no-keys-yet", Sources: []string{nameCheckoutFlags}},
	}}
}
