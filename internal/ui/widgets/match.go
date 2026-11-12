package widgets

import (
	"regexp"
	"strings"
)

// compileFilter turns filter text into a case-insensitive, unanchored matcher.
//
//   - matches any run of characters (including none)
//     _   matches exactly one character
//     \x  matches the character x literally (so \* \_ and \\ are literal)
//
// Everything else matches literally. A lone trailing backslash is literal.
// Empty (or all-space) text returns nil, meaning "match everything".
func compileFilter(text string) *regexp.Regexp {
	return Glob(strings.TrimSpace(text))
}

// Glob compiles text with the same wildcards as the list filter (`*`, `_`,
// backslash escapes) into a case-insensitive, unanchored matcher, without
// trimming it. Empty text returns nil, meaning "match everything".
func Glob(text string) *regexp.Regexp {
	if text == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString("(?is)")
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; r {
		case '*':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		case '\\':
			if i+1 < len(rs) {
				i++
				b.WriteString(regexp.QuoteMeta(string(rs[i])))
			} else {
				b.WriteString(regexp.QuoteMeta(`\`))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	re, err := regexp.Compile(b.String())
	if err != nil { // cannot happen: every literal is quoted
		return nil
	}
	return re
}
