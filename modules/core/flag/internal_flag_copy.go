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

func copyVariations(v *map[string]*any) *map[string]*any {
	if v == nil {
		return nil
	}
	out := make(map[string]*any, len(*v))
	for name, value := range *v {
		if value == nil {
			out[name] = nil
			continue
		}
		c := deepCopyValue(*value)
		out[name] = &c
	}
	return &out
}

func copyMetadata(m *map[string]any) *map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(*m))
	for k, v := range *m {
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
	c := *r
	c.Name = copyPtr(r.Name)
	c.Query = copyPtr(r.Query)
	c.VariationResult = copyPtr(r.VariationResult)
	c.Disable = copyPtr(r.Disable)
	if r.Percentages != nil {
		p := maps.Clone(*r.Percentages)
		c.Percentages = &p
	}
	if r.ProgressiveRollout != nil {
		c.ProgressiveRollout = &ProgressiveRollout{
			Initial: r.ProgressiveRollout.Initial.deepCopy(),
			End:     r.ProgressiveRollout.End.deepCopy(),
		}
	}
	return &c
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
func deepCopyValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = deepCopyValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepCopyValue(e)
		}
		return out
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() { //nolint:exhaustive // only the containers have to be copied
	case reflect.Map:
		if rv.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		for it := rv.MapRange(); it.Next(); {
			out.SetMapIndex(it.Key(), copyReflected(it.Value()))
		}
		return out.Interface()
	case reflect.Slice:
		if rv.IsNil() {
			return v
		}
		out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := range rv.Len() {
			out.Index(i).Set(copyReflected(rv.Index(i)))
		}
		return out.Interface()
	default:
		return v
	}
}

func copyReflected(v reflect.Value) reflect.Value {
	if !v.CanInterface() || (v.Kind() == reflect.Interface && v.IsNil()) {
		return v
	}
	c := deepCopyValue(v.Interface())
	if c == nil {
		return reflect.Zero(v.Type())
	}
	return reflect.ValueOf(c)
}
