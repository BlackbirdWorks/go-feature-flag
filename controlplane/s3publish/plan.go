package s3publish

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// plan validates the request and builds the manifest. It makes no S3 call.
func (p *Publisher) plan(req *Request) (*plan, error) {
	items := make([]planned, 0, len(req.Objects))

	for i := range req.Objects {
		item, err := p.plannedObject(&req.Objects[i])
		if err != nil {
			return nil, err
		}

		items = append(items, item)
	}

	slices.SortStableFunc(items, func(a, b planned) int { return strings.Compare(a.entry.Name, b.entry.Name) })

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: p.cfg.Channel,
		Generation: 1, CreatedAt: req.CreatedAt, Objects: make([]controlplane.ManifestObject, len(items)),
	}
	for i := range items {
		manifest.Objects[i] = *items[i].entry
	}

	for i := range items {
		if items[i].entry.Key == p.cfg.ManifestKey {
			return nil, invalid(label(items[i].entry.Name, items[i].source),
				fmt.Errorf("%w: its key %s is the key of the manifest", ErrManifestKey, items[i].entry.Key))
		}
	}

	err := p.checkLimits(manifest.Objects, func(i int) string { return label(items[i].entry.Name, items[i].source) })
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	err = controlplane.ValidateManifest(manifest, controlplane.WithMaxObjects(p.cfg.MaxObjects))
	if err != nil {
		return nil, invalid("manifest", err)
	}

	err = p.checkManifestSize(manifest)
	if err != nil {
		return nil, err
	}

	warnings, err := p.validateContents(items, manifest)
	if err != nil {
		return nil, err
	}

	return &plan{manifest: manifest, items: items, warnings: warnings}, nil
}

// checkManifestSize refuses a manifest whose marshalled form is over the limit of the reader. It marshals with
// the largest generation, so the answer holds whatever generation is written.
func (p *Publisher) checkManifestSize(manifest *controlplane.Manifest) error {
	probe := *manifest
	probe.Generation = math.MaxUint64

	body, _, err := p.marshalManifest(&probe)
	if err != nil {
		return invalid("manifest", err)
	}

	if int64(len(body)) > p.cfg.MaxManifestBytes {
		return invalid(
			"manifest",
			fmt.Errorf("%w: %d bytes, over the limit of %d bytes", ErrLimit, len(body), p.cfg.MaxManifestBytes),
		)
	}

	return nil
}

// plannedObject computes the checksum, key and manifest entry of one object.
func (p *Publisher) plannedObject(obj *Object) (planned, error) {
	name := label(obj.Name, obj.Source)

	if obj.Format != controlplane.FormatYAML && obj.Format != controlplane.FormatJSON {
		return planned{}, invalid(
			name,
			fmt.Errorf("%w: %q (use yaml or json)", controlplane.ErrUnsupportedFormat, obj.Format),
		)
	}

	sum := controlplane.ChecksumOf(obj.Body)

	key, err := controlplane.ObjectKey(sum, obj.Format)
	if err != nil {
		return planned{}, invalid(name, err)
	}

	raw := sha256.Sum256(obj.Body)

	return planned{
		source: obj.Source, body: obj.Body, format: obj.Format, sha64: base64.StdEncoding.EncodeToString(raw[:]),
		entry: &controlplane.ManifestObject{
			Name: obj.Name, Kind: obj.Kind, Connection: p.cfg.Connection, Key: p.cfg.Prefix + key,
			Format: obj.Format, Checksum: sum, Size: int64(len(obj.Body)),
		},
	}, nil
}

// validateContents decodes every document, runs the Validate functions of the controlplane package and the Check
// hook, and then validates the flag-set documents together.
func (p *Publisher) validateContents(items []planned, manifest *controlplane.Manifest) ([]string, error) {
	var (
		warnings []string
		docs     []*controlplane.FlagSetDocument
		labels   []string
	)

	for i := range items {
		item := &items[i]
		name := label(item.entry.Name, item.source)

		info, doc, err := validateContent(item.entry, item.body)
		if err != nil {
			return nil, invalid(name, err)
		}

		if p.cfg.Check != nil {
			err = p.cfg.Check(&Object{
				Name: item.entry.Name, Kind: item.entry.Kind, Format: item.format, Source: item.source, Body: item.body,
			})
			if err != nil {
				return nil, invalid(name, err)
			}
		}

		if doc != nil {
			docs = append(docs, doc)
			labels = append(labels, strconv.Itoa(len(labels))+"="+name)
		}

		for _, field := range info.UnknownFields {
			warnings = append(warnings, fmt.Sprintf("object %s: unknown field %s", item.entry.Name, field))
		}
	}

	err := controlplane.ValidateFlagSetDocuments(docs, manifest)
	if err != nil {
		return nil, fmt.Errorf("%w: flag-set documents [%s]: %w", ErrInvalid, strings.Join(labels, ", "), err)
	}

	return warnings, nil
}

// validateContent decodes and validates the bytes of one object by its kind. For a flag-set document it returns
// the decoded document; the documents are validated together by the caller. The errors are the ones of the
// controlplane package.
func validateContent(
	entry *controlplane.ManifestObject, body []byte,
) (controlplane.DecodeInfo, *controlplane.FlagSetDocument, error) {
	switch entry.Kind {
	case controlplane.KindFlags:
		if entry.Format == controlplane.FormatTOML {
			// The format package does not read TOML; the relay does. Only the Check hook can look inside.
			return controlplane.DecodeInfo{}, nil, nil
		}

		flags, info, err := controlplane.UnmarshalFlags(body, entry.Format)
		if err != nil {
			return info, nil, fmt.Errorf("decode flags: %w", err)
		}

		err = controlplane.ValidateFlags(flags)
		if err != nil {
			return info, nil, fmt.Errorf("validate flags: %w", err)
		}

		return info, nil, nil
	case controlplane.KindFlagSets:
		doc, info, err := controlplane.UnmarshalFlagSet(body)
		if err != nil {
			return info, nil, fmt.Errorf("decode flag sets: %w", err)
		}

		return info, doc, nil
	}

	return controlplane.DecodeInfo{}, nil, fmt.Errorf("%w: kind %q", controlplane.ErrInvalid, entry.Kind)
}

// label names an object in an error: its name and, when known, where it came from.
func label(name, source string) string {
	if source == "" {
		return name
	}

	return name + " (" + source + ")"
}

func invalid(name string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrInvalid, name, err)
}
