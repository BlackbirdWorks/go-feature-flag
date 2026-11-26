package controlplane_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	abcSum   = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	emptySum = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

func TestChecksumOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want controlplane.Checksum
	}{
		{textABC, textABC, controlplane.Checksum{Algorithm: controlplane.AlgorithmSHA256, Value: abcSum}},
		{"empty input", "", controlplane.Checksum{Algorithm: controlplane.AlgorithmSHA256, Value: emptySum}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, controlplane.ChecksumOf([]byte(tt.in)))
		})
	}
}

func TestVerifyChecksum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		sum     controlplane.Checksum
		wantErr error
		wantGot string
	}{
		{"match", textABC, controlplane.Checksum{Algorithm: controlplane.AlgorithmSHA256, Value: abcSum}, nil, ""},
		{
			"mismatch",
			"abd",
			controlplane.Checksum{Algorithm: controlplane.AlgorithmSHA256, Value: abcSum},
			controlplane.ErrChecksumMismatch,
			"",
		},
		{
			"upper case is invalid, not a mismatch",
			textABC,
			controlplane.Checksum{
				Algorithm: controlplane.AlgorithmSHA256,
				Value:     "BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD",
			},
			controlplane.ErrInvalidChecksum,
			"",
		},
		{
			"other algorithm",
			textABC,
			controlplane.Checksum{Algorithm: algoMD5, Value: abcSum},
			controlplane.ErrUnsupportedChecksum,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := controlplane.VerifyChecksum([]byte(tt.in), tt.sum)
			if tt.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tt.wantErr)

			var mismatch *controlplane.ChecksumMismatchError
			if errors.Is(tt.wantErr, controlplane.ErrChecksumMismatch) {
				require.ErrorAs(t, err, &mismatch)

				want := tt.sum.Value

				assert.Equal(t, want, mismatch.Want)
				assert.Contains(t, err.Error(), "checksum mismatch")
			}
		})
	}
}

func TestObjectKey(t *testing.T) {
	t.Parallel()

	good := controlplane.Checksum{Algorithm: controlplane.AlgorithmSHA256, Value: abcSum}

	tests := []struct { //nolint:govet // fieldalignment: the columns of the table are in reading order
		name    string
		sum     controlplane.Checksum
		ext     string
		want    string
		wantErr error
	}{
		{controlplane.FormatYAML, good, controlplane.FormatYAML, "objects/sha256/ba/" + abcSum + ".yaml", nil},
		{"digits", good, "7z", "objects/sha256/ba/" + abcSum + ".7z", nil},
		{"dotted ext", good, ".json", "", controlplane.ErrInvalidExtension},
		{"traversal", good, "../../x", "", controlplane.ErrInvalidExtension},
		{"slash", good, "a/b", "", controlplane.ErrInvalidExtension},
		{"upper case ext", good, "YAML", "", controlplane.ErrInvalidExtension},
		{"empty ext", good, "", "", controlplane.ErrInvalidExtension},
		{
			"short value",
			controlplane.Checksum{Algorithm: controlplane.AlgorithmSHA256, Value: "a"},
			controlplane.FormatYAML,
			"",
			controlplane.ErrInvalidChecksum,
		},
		{
			"traversing value",
			controlplane.Checksum{Algorithm: controlplane.AlgorithmSHA256, Value: "../../" + abcSum[6:]},
			controlplane.FormatYAML,
			"",
			controlplane.ErrInvalidChecksum,
		},
		{
			"upper case value",
			controlplane.Checksum{Algorithm: controlplane.AlgorithmSHA256, Value: strings.ToUpper(abcSum)},
			controlplane.FormatYAML,
			"",
			controlplane.ErrInvalidChecksum,
		},
		{
			"other algorithm",
			controlplane.Checksum{Algorithm: algoMD5, Value: abcSum},
			controlplane.FormatYAML,
			"",
			controlplane.ErrUnsupportedChecksum,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := controlplane.ObjectKey(tt.sum, tt.ext)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Empty(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
