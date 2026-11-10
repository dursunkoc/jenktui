// Package paramsmerge computes Common Parameters across the jobs of one
// Trigger and resolves the final parameter values to send to each job.
//
// A parameter is common when a parameter with the same name AND the same
// normalised type exists in every selected job (and, for choice parameters,
// the choice sets are equal as sets). Everything else is per-job.
//
// Resolve sends ALL of a job's parameters with their final value (override,
// else common value, else the job's own default) so builds are deterministic
// and do not depend on defaults changing on the server between form and send.
package paramsmerge

import (
	"slices"
	"strings"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

// Param is one parameter in a Plan.
type Param struct {
	Name        string
	Type        string
	Description string
	// Default is the shared default of a common parameter, or the job's own
	// default for a per-job parameter. It is "" when DefaultsDiffer.
	Default        string
	DefaultsDiffer bool // common parameter whose defaults differ across jobs
	Choices        []string
}

// Plan splits the parameters of the selected jobs.
type Plan struct {
	Jobs   []string           // selection order
	Common []Param            // in the order of the first job
	PerJob map[string][]Param // remaining parameters per job, in job order
	// Defaults holds every job's own default per parameter name.
	Defaults map[string]map[string]string
}

func normType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return "string"
	}
	return t
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

// Build computes the Plan. defs maps job -> its parameter definitions; jobs
// gives the order (duplicates ignored). With a single job every parameter is
// common by definition.
func Build(jobs []string, defs map[string][]jenkins.ParamDef) Plan {
	p := Plan{PerJob: map[string][]Param{}, Defaults: map[string]map[string]string{}}
	byJob := map[string]map[string]jenkins.ParamDef{}
	for _, j := range jobs {
		if _, dup := byJob[j]; dup {
			continue
		}
		p.Jobs = append(p.Jobs, j)
		m := map[string]jenkins.ParamDef{}
		for _, d := range defs[j] {
			if _, seen := m[d.Name]; !seen { // first definition of a name wins
				m[d.Name] = d
			}
		}
		byJob[j] = m
		p.Defaults[j] = map[string]string{}
		for n, d := range m {
			p.Defaults[j][n] = d.Default
		}
	}
	if len(p.Jobs) == 0 {
		return p
	}
	isCommon := map[string]bool{}
	first := p.Jobs[0]
	for _, d := range defs[first] {
		if isCommon[d.Name] || !commonAcross(p.Jobs, byJob, byJob[first][d.Name]) {
			continue
		}
		isCommon[d.Name] = true
		fd := byJob[first][d.Name]
		cp := Param{Name: fd.Name, Type: normType(fd.Type), Description: fd.Description, Default: fd.Default, Choices: slices.Clone(fd.Choices)}
		for _, j := range p.Jobs[1:] {
			if byJob[j][d.Name].Default != fd.Default {
				cp.DefaultsDiffer = true
			}
			if cp.Description == "" {
				cp.Description = byJob[j][d.Name].Description
			}
		}
		if cp.DefaultsDiffer {
			cp.Default = ""
		}
		p.Common = append(p.Common, cp)
	}
	for _, j := range p.Jobs {
		var rest []Param
		seen := map[string]bool{}
		for _, d := range defs[j] {
			if isCommon[d.Name] || seen[d.Name] {
				continue
			}
			seen[d.Name] = true
			rest = append(rest, Param{Name: d.Name, Type: normType(d.Type), Description: d.Description, Default: d.Default, Choices: slices.Clone(d.Choices)})
		}
		p.PerJob[j] = rest
	}
	return p
}

func commonAcross(jobs []string, byJob map[string]map[string]jenkins.ParamDef, ref jenkins.ParamDef) bool {
	for _, j := range jobs {
		d, ok := byJob[j][ref.Name]
		if !ok || normType(d.Type) != normType(ref.Type) {
			return false
		}
		if normType(ref.Type) == "choice" && !sameSet(d.Choices, ref.Choices) {
			return false
		}
	}
	return true
}

// Resolve returns, per job, exactly the parameters to send with their final
// values. Precedence: per-job override, then the common value (common
// parameters only; an empty common value for a DefaultsDiffer parameter means
// "not set"), then the job's own default.
func Resolve(plan Plan, common map[string]string, overrides map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(plan.Jobs))
	for _, j := range plan.Jobs {
		vals := map[string]string{}
		for _, c := range plan.Common {
			v := plan.Defaults[j][c.Name]
			if cv, ok := common[c.Name]; ok && !(c.DefaultsDiffer && cv == "") {
				v = cv
			}
			vals[c.Name] = v
		}
		for _, pj := range plan.PerJob[j] {
			vals[pj.Name] = pj.Default
		}
		for n, v := range overrides[j] {
			if _, known := vals[n]; known {
				vals[n] = v
			}
		}
		out[j] = vals
	}
	return out
}
