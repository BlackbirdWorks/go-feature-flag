package publish

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

// The directories of a source tree: one object per file, named after the file.
const (
	flagsDir    = "flags"
	flagSetsDir = "flagsets"
)

// Errors of the source loader. Test them with [errors.Is].
var (
	// ErrUnknownFile is returned for a file that is not a document of the layout.
	ErrUnknownFile = errors.New("unknown file")
	// ErrDuplicateObject is returned when two files give one object name.
	ErrDuplicateObject = errors.New("duplicate object name")
	// ErrNoDocuments is returned when the directory holds no document.
	ErrNoDocuments = errors.New("no documents found")
)

// LoadSource reads the documents of a source tree. The layout:
//
//	flags/<name>.yaml|yml|json      a flag file (kind flags)
//	flagsets/<name>.yaml|yml|json   a flag-set document (kind flagSets)
//
// The object name is the file name without its extension; the format comes from the extension (yml is yaml).
// Names starting with a dot are ignored. Any other file is an error, so a typo is not silently skipped. display is
// put before the relative path in messages. The objects come in path order.
func LoadSource(fsys fs.FS, display string) ([]s3publish.Object, error) {
	var objects []s3publish.Object

	seen := map[string]string{}

	err := fs.WalkDir(fsys, ".", func(rel string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if rel == "." {
			return nil
		}

		if strings.HasPrefix(entry.Name(), ".") {
			return skip(entry)
		}

		if entry.IsDir() {
			if isLayoutDir(rel) {
				return nil
			}

			return fmt.Errorf(
				"%w %q: documents live in %s/ and %s/",
				ErrUnknownFile,
				shown(display, rel),
				flagsDir,
				flagSetsDir,
			)
		}

		obj, err := readDocument(fsys, rel, display)
		if err != nil {
			return err
		}

		if first, dup := seen[obj.Name]; dup {
			return fmt.Errorf("%w %q: %s and %s", ErrDuplicateObject, obj.Name, first, obj.Source)
		}

		seen[obj.Name] = obj.Source
		objects = append(objects, obj)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read source %s: %w", display, err)
	}

	if len(objects) == 0 {
		return nil, fmt.Errorf("%w in %s", ErrNoDocuments, display)
	}

	return objects, nil
}

func skip(entry fs.DirEntry) error {
	if entry.IsDir() {
		return fs.SkipDir
	}

	return nil
}

func isLayoutDir(rel string) bool { return rel == flagsDir || rel == flagSetsDir }

func shown(display, rel string) string { return path.Join(display, rel) }

// readDocument reads one file of the layout.
func readDocument(fsys fs.FS, rel, display string) (s3publish.Object, error) {
	dir, file := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	ext := path.Ext(file)
	name := strings.TrimSuffix(file, ext)

	kind, format, ok := classify(dir, ext)
	if !ok {
		return s3publish.Object{}, fmt.Errorf(
			"%w %q: only %s/*.yaml|yml|json and %s/*.yaml|yml|json are read",
			ErrUnknownFile,
			shown(display, rel),
			flagsDir,
			flagSetsDir,
		)
	}

	body, err := fs.ReadFile(fsys, rel)
	if err != nil {
		return s3publish.Object{}, fmt.Errorf("read %s: %w", shown(display, rel), err)
	}

	return s3publish.Object{Name: name, Kind: kind, Format: format, Source: shown(display, rel), Body: body}, nil
}

// classify maps a directory and an extension to the kind and format of a document.
func classify(dir, ext string) (string, string, bool) {
	var kind string

	switch dir {
	case flagsDir:
		kind = controlplane.KindFlags
	case flagSetsDir:
		kind = controlplane.KindFlagSets
	default:
		return "", "", false
	}

	switch ext {
	case ".yaml", ".yml":
		return kind, controlplane.FormatYAML, true
	case ".json":
		return kind, controlplane.FormatJSON, true
	}

	return "", "", false
}
