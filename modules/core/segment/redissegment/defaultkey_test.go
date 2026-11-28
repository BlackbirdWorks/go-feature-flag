package redissegment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

func TestDefaultKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
		layout redissegment.Layout
		want   string
	}{
		{name: "set with the default prefix", layout: redissegment.LayoutSet, want: "semaphore:segment:{vip}"},
		{name: "empty layout is a set", prefix: "p:", want: "p:{vip}"},
		{name: "keys", prefix: "p:", layout: redissegment.LayoutKeys, want: "p:vip:"},
		{name: "keys with the default prefix", layout: redissegment.LayoutKeys, want: "semaphore:segment:vip:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, redissegment.DefaultKey(tt.prefix, "vip", tt.layout))
		})
	}
}
