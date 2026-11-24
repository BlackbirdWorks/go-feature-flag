package utils

import "slices"

func Contains(s []string, str string) bool {
	return slices.Contains(s, str)
}
