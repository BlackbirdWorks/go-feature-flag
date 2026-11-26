package s3publish_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

func TestDefaultLimitsAreTheOnesOfTheReader(t *testing.T) {
	t.Parallel()

	assert.Equal(t, controlplane.DefaultMaxObjectBytes, int64(16<<20))
	assert.Equal(t, controlplane.DefaultMaxTotalBytes, int64(1<<30))
	assert.Equal(t, controlplane.DefaultMaxManifestBytes, int64(16<<20))
	assert.Equal(t, 8, s3publish.DefaultConcurrency)
}

func TestNewRejectsNegativeLimits(t *testing.T) {
	t.Parallel()

	for _, mutate := range []func(*s3publish.Config){
		func(c *s3publish.Config) { c.MaxObjectBytes = -1 },
		func(c *s3publish.Config) { c.MaxTotalBytes = -1 },
		func(c *s3publish.Config) { c.MaxManifestBytes = -1 },
		func(c *s3publish.Config) { c.MaxObjects = -1 },
		func(c *s3publish.Config) { c.Concurrency = -1 },
	} {
		cfg := testConfig()
		mutate(&cfg)

		pub, err := s3publish.New(s3fake.New(manifestKey), &cfg)
		require.ErrorIs(t, err, s3publish.ErrConfig)
		assert.Nil(t, pub)
	}
}

func TestPublishRefusesWhatTheReaderRejects(t *testing.T) {
	t.Parallel()

	flags := flagsBody(t, "a")
	other := flagsBody(t, "b")
	third := flagsBody(t, "c")
	huge := append(flagsBody(t, "h"), "#"+strings.Repeat("x", int(controlplane.DefaultMaxObjectBytes))+"\n"...)

	tests := []struct {
		mutate    func(*s3publish.Config)
		name      string
		wantInErr string
		objects   []s3publish.Object
	}{
		{
			name:   "an object over the limit names the object and its file",
			mutate: func(c *s3publish.Config) { c.MaxObjectBytes = int64(len(flags)) - 1 },
			objects: []s3publish.Object{
				{
					Name:   "a",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Source: "flags/a.yaml",
					Body:   flags,
				},
			},
			wantInErr: "a (flags/a.yaml): size",
		},
		{
			name: "an empty object",
			objects: []s3publish.Object{
				{
					Name:   "empty",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Source: "flags/empty.yaml",
				},
			},
			wantInErr: "empty (flags/empty.yaml): size 0, an object must not be empty",
		},
		{
			name: "the default limit of the reader is 16 MiB",
			objects: []s3publish.Object{
				{Name: "huge", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: huge},
			},
			wantInErr: "is over the limit of 16777216 bytes",
		},
		{
			name:   "the total of distinct objects is over the limit",
			mutate: func(c *s3publish.Config) { c.MaxTotalBytes = int64(len(flags) + len(other) - 1) },
			objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: flags},
				{Name: "b", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: other},
			},
			wantInErr: "objects total",
		},
		{
			name:   "a manifest over the limit",
			mutate: func(c *s3publish.Config) { c.MaxManifestBytes = 100 },
			objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: flags},
			},
			wantInErr: "bytes, over the limit of 100 bytes",
		},
		{
			name:   msgTooMany,
			mutate: func(c *s3publish.Config) { c.MaxObjects = 2 },
			objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: flags},
				{Name: "b", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: other},
				{Name: "c", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: third},
			},
			wantInErr: "the limit is 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			pub := newPublisher(t, fake, tt.mutate)

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: tt.objects})
			require.ErrorIs(t, err, s3publish.ErrInvalid)
			assert.Contains(t, err.Error(), tt.wantInErr)
			assert.Empty(t, fake.Calls(), "the limits are checked before any S3 call")
			assert.NotContains(t, err.Error(), "flag-a", "content is never part of the error")
		})
	}
}

func TestPublishCountsEqualContentOnceInTheTotal(t *testing.T) {
	t.Parallel()

	body := flagsBody(t, "a")
	fake := s3fake.New(manifestKey)
	pub := newPublisher(t, fake, func(c *s3publish.Config) { c.MaxTotalBytes = int64(len(body)) })

	_, err := pub.Publish(t.Context(), s3publish.Request{Objects: []s3publish.Object{
		{Name: nameOne, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
		{Name: nameTwo, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
	}})
	require.NoError(t, err, "two entries with one checksum are one download for the reader")
}

func TestVerifyAppliesTheLimits(t *testing.T) {
	t.Parallel()

	body := flagsBody(t, "a")
	other := flagsBody(t, "b")

	tests := []struct {
		mutate func(*s3publish.Config)
		name   string
	}{
		{name: "object over the limit", mutate: func(c *s3publish.Config) { c.MaxObjectBytes = int64(len(body)) - 1 }},
		{name: "total over the limit", mutate: func(c *s3publish.Config) { c.MaxTotalBytes = int64(len(body)) }},
		{name: msgTooMany, mutate: func(c *s3publish.Config) { c.MaxObjects = 1 }},
		{name: "manifest over the limit", mutate: func(c *s3publish.Config) { c.MaxManifestBytes = 50 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
				{Name: "b", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: other},
			}})
			require.NoError(t, err)

			rep, err := newPublisher(t, fake, tt.mutate).Verify(t.Context())
			require.NoError(t, err)
			require.Len(t, rep.Problems, 1)
			assert.Equal(t, s3publish.ProblemManifestInvalid, rep.Problems[0].Code)
			assert.Zero(t, rep.Checked, "nothing is read from a manifest that the reader would reject")
		})
	}
}
