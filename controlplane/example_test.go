package controlplane_test

import (
	"fmt"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// The whole publisher flow: marshal the flags, the flag-set document and the manifest, validate them as the
// relay will, then upload the objects first and the manifest last.
func Example() {
	flags := controlplane.Flags{"new-cart": {
		Variations:  map[string]any{"enabled": true, "disabled": false},
		DefaultRule: &controlplane.Rule{Variation: new("disabled")},
		Targeting: []controlplane.Rule{
			{Name: new("pilot"), Query: new(`plan eq "pilot"`), Variation: new("enabled")},
		},
	}}

	err := controlplane.ValidateFlags(flags)
	if err != nil {
		fmt.Println(err)

		return
	}

	body, err := controlplane.MarshalFlags(flags)
	if err != nil {
		fmt.Println(err)

		return
	}

	sum := controlplane.ChecksumOf(body)

	key, err := controlplane.ObjectKey(sum, "yaml")
	if err != nil {
		fmt.Println(err)

		return
	}

	set := &controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{{
		Name: "cart", Sources: []string{"cart-flags"}, APIKeys: []string{"9b2c4f3a-7d1e-4e0b-8a55-3c6d2f1e9a07"},
	}}}

	setBody, err := controlplane.MarshalFlagSet(set)
	if err != nil {
		fmt.Println(err)

		return
	}

	setSum := controlplane.ChecksumOf(setBody)

	setKey, err := controlplane.ObjectKey(setSum, "yaml")
	if err != nil {
		fmt.Println(err)

		return
	}

	manifest := &controlplane.Manifest{
		Channel:    "prod",
		Generation: 1,
		Objects: []controlplane.ManifestObject{
			{
				Name:       "cart-flags",
				Kind:       controlplane.KindFlags,
				Connection: "cart-bucket",
				Key:        key,
				Checksum:   sum,
				Size:       int64(len(body)),
				Format:     controlplane.FormatYAML,
			},
			{
				Name:       "cart-sets",
				Kind:       controlplane.KindFlagSets,
				Connection: "cart-control",
				Key:        setKey,
				Checksum:   setSum,
				Size:       int64(len(setBody)),
				Format:     controlplane.FormatYAML,
			},
		},
	}

	// The same checks the relay runs. An empty header means the current one.
	err = controlplane.ValidateManifest(manifest)
	if err != nil {
		fmt.Println(err)

		return
	}

	err = controlplane.ValidateFlagSet(set, manifest)
	if err != nil {
		fmt.Println(err)

		return
	}

	manifestBody, err := controlplane.MarshalManifest(manifest)
	if err != nil {
		fmt.Println(err)

		return
	}

	// Upload body and setBody first, manifestBody last.
	fmt.Println(manifest.Objects[0].Key)
	fmt.Print(string(manifestBody[:len("apiVersion: semaphore/v1\nkind: Manifest\n")]))
	// Output:
	// objects/sha256/51/51261bbe10356e8c36b7431ed8bf585372f8309d7e7dfa51b971a72fe7e031c3.yaml
	// apiVersion: semaphore/v1
	// kind: Manifest
}

func ExampleUnmarshalManifest() {
	doc := []byte("apiVersion: semaphore/v1\nkind: Manifest\nchannel: prod\ngeneration: 3\nfuture: true\nobjects: []\n")

	manifest, info, err := controlplane.UnmarshalManifest(doc)
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Println(manifest.Channel, manifest.Generation, info.UnknownFields)
	// Output: prod 3 [future]
}

func ExampleUnmarshalManifest_unsupportedVersion() {
	_, _, err := controlplane.UnmarshalManifest([]byte("apiVersion: semaphore/v2\nkind: Manifest\n"))
	fmt.Println(err)
	// Output: controlplane: unsupported apiVersion: got "semaphore/v2", supported "semaphore/v1"
}

func ExampleValidateManifest_withMaxObjects() {
	manifest := &controlplane.Manifest{Channel: "prod", Generation: 1}
	fmt.Println(controlplane.ValidateManifest(manifest, controlplane.WithMaxObjects(10)))
	// Output: <nil>
}

func ExampleVerifyChecksum() {
	sum := controlplane.ChecksumOf([]byte("hello"))
	fmt.Println(controlplane.VerifyChecksum([]byte("hello"), sum))
	fmt.Println(controlplane.VerifyChecksum([]byte("hellO"), sum) != nil)
	// Output:
	// <nil>
	// true
}
