package catalog

import (
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// NamesOf says how each member found in the catalog is shown.
func NamesOf(members []string, cat jenkins.Catalog, parent string) map[string]core.JobName {
	in := map[string]bool{}
	for _, k := range members {
		in[k] = true
	}
	names := map[string]core.JobName{}
	for _, j := range cat.Jobs {
		if in[j.FullName] {
			names[j.FullName] = JobName(j, parent)
		}
	}
	return names
}

// MissingOf flags the members that are not in the catalog.
func MissingOf(members []string, cat jenkins.Catalog) map[string]bool {
	have := map[string]bool{}
	for _, j := range cat.Jobs {
		have[j.FullName] = true
	}
	missing := map[string]bool{}
	for _, k := range members {
		if !have[k] {
			missing[k] = true
		}
	}
	return missing
}
