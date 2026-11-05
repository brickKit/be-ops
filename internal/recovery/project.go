package recovery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// AuditID is the evidence store's component: its schema never goes back (protocol 15, step 3).
const AuditID = "infra/audit"

// Component is one component of the BrickKit project, with the database it owns (none: Schema is empty).
type Component struct {
	ID, Version, Kind string
	Database, Schema  string
}

// Project is a BrickKit project as the recovery reads it: brickkit.yaml's components, each one's database from its
// config/<scope>-<name>.yaml (PG_DATABASE, PG_SCHEMA; $var: references from config/vars.yaml).
type Project struct {
	Dir        string
	Name       string
	Components []Component
}

// LoadProject reads a project directory.
func LoadProject(dir string) (*Project, error) {
	var lock struct {
		Project    string `yaml:"project"`
		Components []struct {
			ID      string `yaml:"id"`
			Version string `yaml:"version"`
			Kind    string `yaml:"kind"`
		} `yaml:"components"`
	}
	if err := readYAML(filepath.Join(dir, "brickkit.yaml"), &lock); err != nil {
		return nil, err
	}
	vars := map[string]string{}
	if err := readYAML(filepath.Join(dir, "config", "vars.yaml"), &vars); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	p := &Project{Dir: dir, Name: lock.Project}
	for _, c := range lock.Components {
		comp := Component{ID: c.ID, Version: c.Version, Kind: c.Kind}
		if c.Kind != "shell" {
			cfg := map[string]string{}
			err := readYAML(filepath.Join(dir, "config", strings.ReplaceAll(c.ID, "/", "-")+".yaml"), &cfg)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			comp.Schema = resolve(cfg["PG_SCHEMA"], vars)
			comp.Database = resolve(cfg["PG_DATABASE"], vars)
			if comp.Schema != "" && comp.Database == "" {
				return nil, fmt.Errorf("%s: PG_SCHEMA without PG_DATABASE", c.ID)
			}
		}
		p.Components = append(p.Components, comp)
	}
	sort.Slice(p.Components, func(i, j int) bool { return p.Components[i].ID < p.Components[j].ID })
	return p, nil
}

// Restored are the components whose databases go back to T: every one with a database but the evidence store.
func (p *Project) Restored() []Component {
	var out []Component
	for _, c := range p.Components {
		if c.Schema != "" && c.ID != AuditID {
			out = append(out, c)
		}
	}
	return out
}

func resolve(v string, vars map[string]string) string {
	if name, ok := strings.CutPrefix(v, "$var:"); ok {
		return vars[name]
	}
	return v
}

func readYAML(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

var phaseLine = regexp.MustCompile(`(?m)^RECOVERY_PHASE:.*\n?`)

// SetPhase writes the recovery phase into the shared variables (config/vars.yaml; A140), or removes it for running;
// the rest of the file stays as it is. Every component reads it at its next start (be-protocol P1.22).
func (p *Project) SetPhase(phase string) error {
	path := filepath.Join(p.Dir, "config", "vars.yaml")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	s := phaseLine.ReplaceAllString(string(raw), "")
	if phase != "" {
		if s != "" && !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		s += "RECOVERY_PHASE: " + phase + "\n"
	}
	return os.WriteFile(path, []byte(s), 0o644)
}
