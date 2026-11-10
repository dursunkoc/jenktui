// Package branchfilter implements the Branch Filter: case-sensitive glob
// patterns matched against decoded branch names of Multibranch Projects.
//
// Glob syntax (deliberately small):
//
//   - any run of characters, including '/' and the empty run
//     ?       exactly one character (any, including '/')
//     [abc]   one character from the set; ranges a-z; [!abc] or [^abc] negates
//     \x      the literal character x
//
// Everything else matches itself, so an exact branch name is a pattern.
// Unlike path.Match, '*' crosses '/' so "release/*" matches "release/1.2/hotfix".
package branchfilter

import (
	"errors"
	"fmt"
)

type tokKind int

const (
	tokLit tokKind = iota
	tokAny
	tokStar
	tokClass
)

type rng struct{ lo, hi rune }

type token struct {
	kind   tokKind
	r      rune
	neg    bool
	ranges []rng
}

// Validate reports whether pattern is a well-formed, non-empty glob.
func Validate(pattern string) error {
	_, err := compile(pattern)
	return err
}

// MatchPattern reports whether name matches pattern. A malformed pattern never
// matches.
func MatchPattern(pattern, name string) bool {
	toks, err := compile(pattern)
	if err != nil {
		return false
	}
	return match(toks, []rune(name))
}

func compile(pattern string) ([]token, error) {
	if pattern == "" {
		return nil, errors.New("empty pattern")
	}
	rs := []rune(pattern)
	var toks []token
	for i := 0; i < len(rs); i++ {
		switch c := rs[i]; c {
		case '*':
			if n := len(toks); n > 0 && toks[n-1].kind == tokStar {
				continue // collapse "**"
			}
			toks = append(toks, token{kind: tokStar})
		case '?':
			toks = append(toks, token{kind: tokAny})
		case '\\':
			i++
			if i >= len(rs) {
				return nil, fmt.Errorf("pattern %q ends with a lone backslash", pattern)
			}
			toks = append(toks, token{kind: tokLit, r: rs[i]})
		case '[':
			t := token{kind: tokClass}
			i++
			if i < len(rs) && (rs[i] == '!' || rs[i] == '^') {
				t.neg = true
				i++
			}
			closed := false
			for first := true; i < len(rs); first = false {
				if rs[i] == ']' && !first {
					closed = true
					break
				}
				lo := rs[i]
				if lo == '\\' {
					i++
					if i >= len(rs) {
						break
					}
					lo = rs[i]
				}
				hi := lo
				if i+2 < len(rs) && rs[i+1] == '-' && rs[i+2] != ']' {
					hi = rs[i+2]
					if hi == '\\' {
						if i+3 >= len(rs) {
							i = len(rs)
							break
						}
						hi = rs[i+3]
						i++
					}
					i += 2
					if lo > hi {
						return nil, fmt.Errorf("pattern %q has an inverted range %c-%c", pattern, lo, hi)
					}
				}
				t.ranges = append(t.ranges, rng{lo, hi})
				i++
			}
			if !closed {
				return nil, fmt.Errorf("pattern %q has an unclosed '['", pattern)
			}
			toks = append(toks, t)
		default:
			toks = append(toks, token{kind: tokLit, r: c})
		}
	}
	return toks, nil
}

func (t token) matches(c rune) bool {
	switch t.kind {
	case tokLit:
		return t.r == c
	case tokAny:
		return true
	case tokClass:
		in := false
		for _, r := range t.ranges {
			if c >= r.lo && c <= r.hi {
				in = true
				break
			}
		}
		return in != t.neg
	}
	return false
}

// match is an iterative matcher with single-star backtracking (linear space,
// O(len(toks)*len(name)) worst case).
func match(toks []token, name []rune) bool {
	ti, ni := 0, 0
	starT, starN := -1, 0
	for ni < len(name) {
		switch {
		case ti < len(toks) && toks[ti].kind == tokStar:
			starT, starN = ti, ni
			ti++
		case ti < len(toks) && toks[ti].matches(name[ni]):
			ti++
			ni++
		case starT >= 0:
			starN++
			ti, ni = starT+1, starN
		default:
			return false
		}
	}
	for ti < len(toks) && toks[ti].kind == tokStar {
		ti++
	}
	return ti == len(toks)
}
