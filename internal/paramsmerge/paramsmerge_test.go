package paramsmerge

import (
	"reflect"
	"testing"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

func pd(name, typ, def string, choices ...string) jenkins.ParamDef {
	return jenkins.ParamDef{Name: name, Type: typ, Default: def, Choices: choices}
}

func names(ps []Param) []string {
	var o []string
	for _, p := range ps {
		o = append(o, p.Name)
	}
	return o
}

func TestBuild(t *testing.T) {
	tests := []struct {
		name    string
		jobs    []string
		defs    map[string][]jenkins.ParamDef
		common  []string
		perJob  map[string][]string
		differs []string
	}{
		{"no jobs", nil, nil, nil, nil, nil},
		{"single job all common", []string{"a"}, map[string][]jenkins.ParamDef{"a": {pd("X", "string", "1")}}, []string{"X"}, map[string][]string{"a": nil}, nil},
		{"same name+type", []string{"a", "b"}, map[string][]jenkins.ParamDef{
			"a": {pd("ENV", "string", "dev"), pd("A", "string", "")},
			"b": {pd("ENV", "string", "dev"), pd("B", "bool", "true")}},
			[]string{"ENV"}, map[string][]string{"a": {"A"}, "b": {"B"}}, nil},
		{"same name different type is per-job", []string{"a", "b"}, map[string][]jenkins.ParamDef{
			"a": {pd("V", "string", "")}, "b": {pd("V", "bool", "false")}},
			nil, map[string][]string{"a": {"V"}, "b": {"V"}}, nil},
		{"missing in one job", []string{"a", "b", "c"}, map[string][]jenkins.ParamDef{
			"a": {pd("V", "string", "")}, "b": {pd("V", "string", "")}, "c": {}},
			nil, map[string][]string{"a": {"V"}, "b": {"V"}, "c": nil}, nil},
		{"differing defaults", []string{"a", "b"}, map[string][]jenkins.ParamDef{
			"a": {pd("V", "string", "1")}, "b": {pd("V", "string", "2")}},
			[]string{"V"}, map[string][]string{"a": nil, "b": nil}, []string{"V"}},
		{"equal choice sets in other order are common", []string{"a", "b"}, map[string][]jenkins.ParamDef{
			"a": {pd("C", "choice", "x", "x", "y")}, "b": {pd("C", "choice", "y", "y", "x")}},
			[]string{"C"}, map[string][]string{"a": nil, "b": nil}, []string{"C"}},
		{"different choice sets are per-job", []string{"a", "b"}, map[string][]jenkins.ParamDef{
			"a": {pd("C", "choice", "x", "x", "y")}, "b": {pd("C", "choice", "x", "x", "z")}},
			nil, map[string][]string{"a": {"C"}, "b": {"C"}}, nil},
		{"type compare is normalised", []string{"a", "b"}, map[string][]jenkins.ParamDef{
			"a": {pd("V", "String", "")}, "b": {pd("V", "string", "")}},
			[]string{"V"}, map[string][]string{"a": nil, "b": nil}, nil},
		{"duplicate jobs ignored", []string{"a", "a"}, map[string][]jenkins.ParamDef{"a": {pd("V", "string", "")}},
			[]string{"V"}, map[string][]string{"a": nil}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := Build(tc.jobs, tc.defs)
			if !reflect.DeepEqual(names(p.Common), tc.common) {
				t.Errorf("common = %v, want %v", names(p.Common), tc.common)
			}
			for j, want := range tc.perJob {
				if !reflect.DeepEqual(names(p.PerJob[j]), want) {
					t.Errorf("perjob[%s] = %v, want %v", j, names(p.PerJob[j]), want)
				}
			}
			var diff []string
			for _, c := range p.Common {
				if c.DefaultsDiffer {
					diff = append(diff, c.Name)
					if c.Default != "" {
						t.Errorf("differing default must be empty, got %q", c.Default)
					}
				}
			}
			if !reflect.DeepEqual(diff, tc.differs) {
				t.Errorf("differs = %v, want %v", diff, tc.differs)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	defs := map[string][]jenkins.ParamDef{
		"a": {pd("ENV", "string", "dev"), pd("V", "string", "1"), pd("ONLYA", "string", "x")},
		"b": {pd("ENV", "string", "dev"), pd("V", "string", "2")},
	}
	plan := Build([]string{"a", "b"}, defs)
	tests := []struct {
		name      string
		common    map[string]string
		overrides map[string]map[string]string
		want      map[string]map[string]string
	}{
		{"nothing set: job defaults", nil, nil, map[string]map[string]string{
			"a": {"ENV": "dev", "V": "1", "ONLYA": "x"}, "b": {"ENV": "dev", "V": "2"}}},
		{"common applies to all", map[string]string{"ENV": "prod", "V": "9"}, nil, map[string]map[string]string{
			"a": {"ENV": "prod", "V": "9", "ONLYA": "x"}, "b": {"ENV": "prod", "V": "9"}}},
		{"empty common for differing default means unset", map[string]string{"ENV": "dev", "V": ""}, nil, map[string]map[string]string{
			"a": {"ENV": "dev", "V": "1", "ONLYA": "x"}, "b": {"ENV": "dev", "V": "2"}}},
		{"empty common for equal default is a real empty", map[string]string{"ENV": ""}, nil, map[string]map[string]string{
			"a": {"ENV": "", "V": "1", "ONLYA": "x"}, "b": {"ENV": "", "V": "2"}}},
		{"override beats common, per-job param override, unknown ignored", map[string]string{"ENV": "prod"},
			map[string]map[string]string{"a": {"ENV": "qa", "ONLYA": "y", "NOPE": "z"}},
			map[string]map[string]string{
				"a": {"ENV": "qa", "V": "1", "ONLYA": "y"}, "b": {"ENV": "prod", "V": "2"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(plan, tc.common, tc.overrides)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v\nwant %v", got, tc.want)
			}
		})
	}
}
