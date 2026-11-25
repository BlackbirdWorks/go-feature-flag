package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

func TestContains(t *testing.T) {
	t.Parallel()

	type args struct {
		str string
		s   []string
	}

	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Should contains an element",
			args: args{
				s:   []string{"aa", "ab", "abc", "abcd", "abcde"},
				str: "aa",
			},
			want: true,
		},
		{
			name: "Should not contains an element",
			args: args{
				s:   []string{"aa", "ab", "abc", "abcd", "abcde"},
				str: "a",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, utils.Contains(tt.args.s, tt.args.str), "Contains()")
		})
	}
}
