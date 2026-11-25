package utils

import "slices"

// Contains reports whether str is an element of s.
func Contains(s []string, str string) bool {
	return slices.Contains(s, str)
}
