package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

const (
	e56F628 = "e56f628e-9817-498f-ae38-4961e9c2bb21"
)

// FuzzBuildHash checks that the allocation-free hash matches Hash(flag+key)
// for every input, including the zero maxPercentage short-circuit, and that a
// non-zero maxPercentage always yields a bucket in range.
func FuzzBuildHash(f *testing.F) {
	f.Add(myFlag, e56F628, uint32(100000))
	f.Add("", e56F628, uint32(100000))
	f.Add("", "", uint32(100000))
	f.Add("", "", uint32(0))
	f.Add("flàg", "ключ", uint32(1000))
	f.Add("flag", user2, uint32(4294967295))
	f.Fuzz(func(t *testing.T, flagKey, bucketingKey string, maxPercentage uint32) {
		got := utils.BuildHash(flagKey, bucketingKey, maxPercentage)
		if maxPercentage == 0 {
			assert.Equal(t, uint32(0), got)

			return
		}

		assert.Equal(t, utils.Hash(flagKey+bucketingKey)%maxPercentage, got)
		assert.Less(t, got, maxPercentage)
	})
}
