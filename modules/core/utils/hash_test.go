package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/utils"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestHash(t *testing.T) {
	t.Parallel()

	type args struct {
		s string
	}

	tests := []struct {
		name string
		args args
		want uint32
	}{
		{
			name: "standard hash",
			args: args{s: "flagNameUserKey"},
			want: 3946001934,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, utils.Hash(tt.args.s), "Hash()")
		})
	}
}

func TestBuildHash(t *testing.T) {
	t.Parallel()

	type args struct {
		flagKey       string
		bucketingKey  string
		maxPercentage uint32
	}

	tests := []struct {
		name string
		args args
		want uint32
	}{
		{
			name: "all fields",
			args: args{
				flagKey:       fixtures.FlagKeyMy,
				bucketingKey:  userKeyUUID,
				maxPercentage: 100000,
			},
			want: 70272,
		},
		{
			name: "empty flag key",
			args: args{
				flagKey:       "",
				bucketingKey:  userKeyUUID,
				maxPercentage: 100000,
			},
			want: 74237,
		},
		{
			name: "empty flag key and bucketing key",
			args: args{
				flagKey:       "",
				bucketingKey:  "",
				maxPercentage: 100000,
			},
			want: 36261,
		},
		{
			name: "empty flag key and bucketing key",
			args: args{
				flagKey:       "",
				bucketingKey:  "",
				maxPercentage: 0,
			},
			want: 0,
		},
		{
			name: "multibyte flag key and bucketing key",
			args: args{
				flagKey:       "flàg",
				bucketingKey:  "ключ",
				maxPercentage: 1000,
			},
			want: 605,
		},
		{
			name: "max percentage is the largest uint32",
			args: args{
				flagKey:       "flag",
				bucketingKey:  fixtures.ContextKindUser,
				maxPercentage: 4294967295,
			},
			want: 3459711972,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := utils.BuildHash(tt.args.flagKey, tt.args.bucketingKey, tt.args.maxPercentage)
			assert.Equal(t, tt.want, got)
		})
	}
}
