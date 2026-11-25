package flag

import (
	"maps"
	"reflect"
)

// DeepCopy returns a copy of the flag that shares no mutable data with it: the variations (and the
// values they hold), the rules, the rollouts, the scheduled steps and the metadata are copied, so
// changing the copy never changes the original.
func (f *InternalFlag) DeepCopy() *InternalFlag {
	if f == nil {
		return nil
	}

	return &InternalFlag{
		Variations:      copyVariations(f.Variations),
		Rules:           copyRules(f.Rules),
		BucketingKey:    copyPtr(f.BucketingKey),
		DefaultRule:     f.DefaultRule.deepCopy(),
		Experimentation: f.Experimentation.deepCopy(),
		Scheduled:       copyScheduled(f.Scheduled),
		TrackEvents:     copyPtr(f.TrackEvents),
		Disable:         copyPtr(f.Disable),
		Version:         copyPtr(f.Version),
		Metadata:        copyMetadata(f.Metadata),
	}
}

func copyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}

	v := *p

	return &v
}

func copyVariations(variations *map[string]*any) *map[string]*any {
	if variations == nil {
		return nil
	}

	out := make(map[string]*any, len(*variations))
	for name, value := range *variations {
		if value == nil {
			out[name] = nil

			continue
		}

		c := deepCopyValue(*value)
		out[name] = &c
	}

	return &out
}

func copyMetadata(metadata *map[string]any) *map[string]any {
	if metadata == nil {
		return nil
	}

	out := make(map[string]any, len(*metadata))
	for k, v := range *metadata {
		out[k] = deepCopyValue(v)
	}

	return &out
}

func copyRules(rules *[]Rule) *[]Rule {
	if rules == nil {
		return nil
	}

	out := make([]Rule, len(*rules))
	for i := range *rules {
		out[i] = *(*rules)[i].deepCopy()
	}

	return &out
}

func copyScheduled(steps *[]ScheduledStep) *[]ScheduledStep {
	if steps == nil {
		return nil
	}

	out := make([]ScheduledStep, len(*steps))
	for i := range *steps {
		out[i] = ScheduledStep{
			InternalFlag: *(*steps)[i].DeepCopy(),
			Date:         copyPtr((*steps)[i].Date),
		}
	}

	return &out
}

func (r *Rule) deepCopy() *Rule {
	if r == nil {
		return nil
	}

	copied := *r
	copied.Name = copyPtr(r.Name)
	copied.Query = copyPtr(r.Query)
	copied.VariationResult = copyPtr(r.VariationResult)

	copied.Disable = copyPtr(r.Disable)
	if r.Percentages != nil {
		p := maps.Clone(*r.Percentages)
		copied.Percentages = &p
	}

	if r.ProgressiveRollout != nil {
		copied.ProgressiveRollout = &ProgressiveRollout{
			Initial: r.ProgressiveRollout.Initial.deepCopy(),
			End:     r.ProgressiveRollout.End.deepCopy(),
		}
	}

	return &copied
}

func (p *ProgressiveRolloutStep) deepCopy() *ProgressiveRolloutStep {
	if p == nil {
		return nil
	}

	return &ProgressiveRolloutStep{
		Variation:  copyPtr(p.Variation),
		Percentage: copyPtr(p.Percentage),
		Date:       copyPtr(p.Date),
	}
}

func (e *ExperimentationRollout) deepCopy() *ExperimentationRollout {
	if e == nil {
		return nil
	}

	return &ExperimentationRollout{Start: copyPtr(e.Start), End: copyPtr(e.End)}
}

// deepCopyValue copies the maps and the slices of a value read from a configuration (variation values and
// metadata), at any depth. The other values (strings, numbers, booleans, ...) are immutable and are returned as is.
func deepCopyValue(val any) any {
	switch typed := val.(type) {
	case nil:
		return nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, e := range typed {
			out[k] = deepCopyValue(e)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, e := range typed {
			out[i] = deepCopyValue(e)
		}

		return out
	}

	reflVal := reflect.ValueOf(val)
	switch kind := reflVal.Kind(); {
	case kind == reflect.Map:
		if reflVal.IsNil() {
			return val
		}

		out := reflect.MakeMapWithSize(reflVal.Type(), reflVal.Len())
		for it := reflVal.MapRange(); it.Next(); {
			out.SetMapIndex(it.Key(), copyReflected(it.Value()))
		}

		return out.Interface()
	case kind == reflect.Slice:
		if reflVal.IsNil() {
			return val
		}

		out := reflect.MakeSlice(reflVal.Type(), reflVal.Len(), reflVal.Len())
		for i := range reflVal.Len() {
			out.Index(i).Set(copyReflected(reflVal.Index(i)))
		}

		return out.Interface()
	}

	return val
}

func copyReflected(val reflect.Value) reflect.Value {
	if !val.CanInterface() || (val.Kind() == reflect.Interface && val.IsNil()) {
		return val
	}

	c := deepCopyValue(val.Interface())
	if c == nil {
		return reflect.Zero(val.Type())
	}

	return reflect.ValueOf(c)
}
