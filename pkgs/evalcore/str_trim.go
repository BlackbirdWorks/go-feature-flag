package evalcore

import "strings"

// StrTrim returns query as a single line, for the rules-format grammar: the leading spaces of every line, the line ends
// (LF or CRLF) and the blank lines are removed. Two lines are joined with a space unless the text already has a
// separator there (the previous line ends with a space) or the join cannot merge two tokens (after an opening
// bracket, before a closing bracket or a comma). A line break inside a quoted value becomes exactly one space (a space that already ends the line is reused).
// A query on one line only loses its leading spaces.
//
// It must not be used on a JsonLogic query: that is JSON, which ignores whitespace between tokens and rejects a
// newline inside a string, so it is read as written.
func StrTrim(query string) string {
	if !strings.Contains(query, "\n") {
		return strings.TrimLeft(query, " ")
	}

	var (
		joined  strings.Builder
		inQuote bool
	)

	joined.Grow(len(query))

	for line := range strings.SplitSeq(query, "\n") {
		line = strings.TrimLeft(strings.TrimSuffix(line, "\r"), " ")

		switch {
		case inQuote:
			// the spaces around a line break inside a quoted value (and any blank lines) collapse to one space,
			// whatever the neighbouring punctuation: an ending space of the previous line is reused
			if joined.String()[joined.Len()-1] != ' ' {
				joined.WriteByte(' ')
			}
		case line == "":
			continue
		case joined.Len() > 0 && needsSeparator(joined.String()[joined.Len()-1], line[0]):
			joined.WriteByte(' ')
		}

		joined.WriteString(line)

		inQuote = endsInQuote(line, inQuote)
	}

	return joined.String()
}

// endsInQuote reports whether a quoted value is still open at the end of line, given that one was open at its start.
func endsInQuote(line string, inQuote bool) bool {
	for i := 0; i < len(line); i++ {
		switch {
		case inQuote && line[i] == '\\':
			i++
		case line[i] == '"':
			inQuote = !inQuote
		}
	}

	return inQuote
}

// needsSeparator is true when the lines would merge their tokens, or change the text, if joined as they are.
func needsSeparator(last, first byte) bool {
	switch {
	case last == ' ' || last == '(' || last == '[':
		return false
	case first == ')' || first == ']' || first == ',':
		return false
	default:
		return true
	}
}
