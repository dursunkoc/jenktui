package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// PresetFile is the presets.toml at Path. Every call reads the file, and
// Save and Delete write it back, so two jenktui processes do not overwrite
// each other's presets with stale data. It knows nothing about parameter
// types: passwords are removed by the caller before Save.
type PresetFile struct{ Path string }

type presetEntry struct {
	Job    string            `toml:"job"`
	Name   string            `toml:"name"`
	Values map[string]string `toml:"values"`
}

type presetDoc struct {
	Items []presetEntry `toml:"preset"`
}

func (p PresetFile) read() (*presetDoc, error) {
	b, err := os.ReadFile(p.Path)
	if errors.Is(err, os.ErrNotExist) {
		return &presetDoc{}, nil
	}
	if err != nil {
		return nil, err
	}
	var d presetDoc
	if err := toml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p.Path, err)
	}
	return &d, nil
}

func (p PresetFile) write(d *presetDoc) error {
	b, err := toml.Marshal(d)
	if err != nil {
		return err
	}
	return writeFile(p.Path, b)
}

func cloneValues(v map[string]string) map[string]string {
	out := make(map[string]string, len(v))
	for k, s := range v {
		out[k] = s
	}
	return out
}

// Load returns job -> preset name -> values for the given jobs. A job without
// presets is absent from the result.
func (p PresetFile) Load(jobs []string) (map[string]map[string]map[string]string, error) {
	d, err := p.read()
	if err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		want[j] = true
	}
	out := map[string]map[string]map[string]string{}
	for _, e := range d.Items {
		if !want[e.Job] {
			continue
		}
		if out[e.Job] == nil {
			out[e.Job] = map[string]map[string]string{}
		}
		// Hand edit: names differing only by case. The later one wins.
		for n := range out[e.Job] {
			if strings.EqualFold(n, e.Name) {
				delete(out[e.Job], n)
			}
		}
		out[e.Job][e.Name] = cloneValues(e.Values)
	}
	return out, nil
}

// Save stores values (job -> values) under name, replacing a preset of the
// same name (letter case ignored) for each of those jobs. A file that does
// not parse is returned as an error and left untouched.
func (p PresetFile) Save(name string, values map[string]map[string]string) error {
	if name == "" {
		return errors.New("preset name is required")
	}
	d, err := p.read()
	if err != nil {
		return err
	}
	jobs := make([]string, 0, len(values))
	for j := range values {
		jobs = append(jobs, j)
	}
	sort.Strings(jobs)
	for _, j := range jobs {
		entry := presetEntry{Job: j, Name: name, Values: cloneValues(values[j])}
		kept := d.Items[:0:0]
		placed := false
		for _, e := range d.Items {
			if e.Job == j && strings.EqualFold(e.Name, name) {
				if !placed {
					kept = append(kept, entry)
					placed = true
				}
				continue
			}
			kept = append(kept, e)
		}
		if !placed {
			kept = append(kept, entry)
		}
		d.Items = kept
	}
	return p.write(d)
}

// Delete removes the preset of that name (letter case ignored) for each job.
func (p PresetFile) Delete(name string, jobs []string) error {
	d, err := p.read()
	if err != nil {
		return err
	}
	in := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		in[j] = true
	}
	kept := d.Items[:0:0]
	for _, e := range d.Items {
		if in[e.Job] && strings.EqualFold(e.Name, name) {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == len(d.Items) {
		return nil
	}
	d.Items = kept
	return p.write(d)
}
