package helper

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/blackbirdworks/semaphore/cmdhelpers/manifest/model"
)

var (
	errImpossibleToFindType  = errors.New("impossible to find type, no variations found")
	errImpossibleToFindType2 = errors.New("impossible to find type")
	errUnknownType           = errors.New("unknown type")
)

// FlagTypeFromVariations returns the flag type inferred from the values of the variations.
func FlagTypeFromVariations(variations map[string]*any) (model.FlagType, error) {
	if variations == nil {
		return "", errImpossibleToFindType
	}

	variationTypes := collectVariationTypes(variations)

	// we found the type and return it
	if len(variationTypes) == 1 {
		for key := range variationTypes {
			return key, nil
		}
	}

	_, okFloat := variationTypes[model.FlagTypeFloat]

	_, okInteger := variationTypes[model.FlagTypeInteger]
	if len(variationTypes) == 2 && okInteger && okFloat {
		// we need to check if it is a float or an integer
		return numberFlagType(variations)
	}

	return "", errImpossibleToFindType2
}

// collectVariationTypes returns the set of the flag types of the variations that have a known type.
func collectVariationTypes(variations map[string]*any) map[model.FlagType]any {
	variationTypes := make(map[model.FlagType]any, len(variations))
	for _, val := range variations {
		if val == nil {
			// we skip if value is nil
			continue
		}

		switch (*val).(type) {
		case bool:
			variationTypes[model.FlagTypeBoolean] = any(nil)
		case string:
			variationTypes[model.FlagTypeString] = any(nil)
		case int:
			variationTypes[model.FlagTypeInteger] = any(nil)
		case float64:
			variationTypes[model.FlagTypeFloat] = any(nil)
		case map[string]any:
			variationTypes[model.FlagTypeObject] = any(nil)
		default:
			// do nothing here
			continue
		}
	}

	return variationTypes
}

// numberFlagType is float if one of the variations is a float, integer otherwise.
func numberFlagType(variations map[string]*any) (model.FlagType, error) {
	for _, variation := range variations {
		if variation == nil {
			// we skip if value is nil
			continue
		}

		kind, err := numberType(*variation)
		if err != nil {
			return "", err
		}

		if kind == "float" {
			return model.FlagTypeFloat, nil
		}
	}

	return model.FlagTypeInteger, nil
}

func numberType(value any) (string, error) {
	val := reflect.ValueOf(value)
	switch val.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer", nil
	case reflect.Float32, reflect.Float64:
		// Check if the float has a whole number value.
		floatVal := val.Float()
		if floatVal == float64(int64(floatVal)) {
			return "integer", nil
		}

		return "float", nil
	default:
		return "", fmt.Errorf("%w %v", errUnknownType, val.Kind())
	}
}
