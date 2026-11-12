package widgets

import "testing"

func TestFilterWildcards(t *testing.T) {
	keys := []string{"team/app/build", "team/app/deploy", "my_job", "myXjob", "a*b", "back\\slash", "Other/Lib"}
	tests := []struct {
		filter string
		want   []string
	}{
		{"", keys},
		{"   ", keys},
		{"APP", []string{"team/app/build", "team/app/deploy"}}, // case-insensitive substring
		{"team*build", []string{"team/app/build"}},             // * spans any characters
		{"app/*y", []string{"team/app/deploy"}},                // * then literal
		{"my_job", []string{"my_job", "myXjob"}},               // _ is exactly one char
		{`my\_job`, []string{"my_job"}},                        // \_ is a literal underscore
		{`a\*b`, []string{"a*b"}},                              // \* is a literal star
		{"a*b", []string{"team/app/build", "a*b"}},             // real wildcard: an a, then later a b
		{`k\\s`, []string{"back\\slash"}},                      // \\ is a literal backslash
		{`back\`, []string{"back\\slash"}},                     // a lone trailing \ is literal
		{"zzz", nil},
		{"(", nil}, // regex metachars stay literal
	}
	for _, tc := range tests {
		l := NewList(newList(Options{}).theme, Options{})
		var items []Item
		for _, k := range keys {
			items = append(items, Item{Key: k})
		}
		l.SetItems(items)
		l.filter = tc.filter
		var got []string
		for _, i := range l.visible() {
			got = append(got, l.items[i].Key)
		}
		if len(got) != len(tc.want) {
			t.Errorf("filter %q: got %v want %v", tc.filter, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("filter %q: got %v want %v", tc.filter, got, tc.want)
				break
			}
		}
	}
}

func TestGlobKeepsSpacesAndMatchesNothingWhenEmpty(t *testing.T) {
	if Glob("") != nil {
		t.Fatal("empty is nil")
	}
	re := Glob(" a_c ")
	if !re.MatchString("x a-c y") || re.MatchString("xa-cy") {
		t.Fatal("spaces are literal, _ is one character")
	}
}
