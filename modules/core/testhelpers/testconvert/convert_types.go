package testconvert

import "time"

// Bool returns a pointer to the bool value passed in.
//
//go:fix inline
func Bool(v bool) *bool {
	return new(v)
}

// Time returns a pointer to the Time value passed in.
//
//go:fix inline
func Time(t time.Time) *time.Time {
	return new(t)
}

// Float64 returns a pointer to the float64 value passed in.
//
//go:fix inline
func Float64(t float64) *float64 {
	return new(t)
}

// Int returns a pointer to the float64 value passed in.
//
//go:fix inline
func Int(t int) *int {
	return new(t)
}

// Interface returns a pointer to the interface value passed in.
//
//go:fix inline
func Interface(v any) *any {
	return new(v)
}

// String returns a pointer to the string value passed in.
//
//go:fix inline
func String(v string) *string {
	return new(v)
}
