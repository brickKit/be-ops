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

// properties is a component's configSchema properties from its manifest in BrickKit's cache; ok false: not there (a
// shell member compiled in, or nothing to judge by).
func (p *Project) properties(c Component) (props map[string]struct {
	Enum []string `yaml:"enum"`
}, ok bool) {
	var m struct {
		ConfigSchema struct {
			Properties map[string]struct {
				Enum []string `yaml:"enum"`
			} `yaml:"properties"`
		} `yaml:"configSchema"`
	}
	if err := readYAML(filepath.Join(p.Dir, ".brickkit", "manifests", c.ID, c.Version, "component.yaml"), &m); err != nil {
		return nil, false
	}
	return m.ConfigSchema.Properties, true
}

// Unsupported are the components run on their own whose version cannot run in phase: its manifest declares no
// RECOVERY_PHASE (a Python member until be-sdk-python implements P1.22), or an enum without phase (a version from
// before the phase): they would not start, so a recovery stops them (Hold).
func (p *Project) Unsupported(phase string) []string {
	var out []string
	for _, c := range p.Components {
		if c.Kind == "shell" {
			continue // its members are compiled in with one SDK
		}
		props, ok := p.properties(c)
		if !ok {
			continue
		}
		key, declared := props["RECOVERY_PHASE"]
		if !declared || phase != "" && len(key.Enum) > 0 && !contains(key.Enum, phase) {
			out = append(out, c.ID)
		}
	}
	return out
}

// Senders are the components that send external commands (their manifest declares EXTERNAL_COMMANDS_QUERY_ONLY,
// be-protocol P13.10): step 7 reconciles them.
func (p *Project) Senders() []string {
	var out []string
	for _, c := range p.Components {
		if props, ok := p.properties(c); ok {
			if _, sends := props["EXTERNAL_COMMANDS_QUERY_ONLY"]; sends {
				out = append(out, c.ID)
			}
		}
	}
	return out
}

// Checked are the components with a database that run in phase: step 8 asks each for its checks (CheckService,
// be-protocol P14.1), the evidence store included.
func (p *Project) Checked(phase string) []string {
	held := p.Unsupported(phase)
	var out []string
	for _, c := range p.Components {
		if c.Schema != "" && !contains(held, c.ID) {
			out = append(out, c.ID)
		}
	}
	return out
}

// holdMark is the comment on the lines Hold writes, so that only those are taken out again.
const holdMark = "# be-ops recover: cannot be frozen (no recovery mode)"

// Hold stops the components that cannot be frozen while a recovery runs (on: mode: disable in the deploy file, which
// BrickKit's next up honours) and starts them again after (off: the lines Hold wrote taken out).
func (p *Project) Hold(ids []string, on bool) error {
	path := filepath.Join(p.Dir, "deploy.yaml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) && len(ids) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.Contains(l, holdMark) {
			continue
		}
		out = append(out, l)
		if !on {
			continue
		}
		for _, id := range ids {
			if strings.TrimSpace(l) == "- id: "+id && strings.HasPrefix(l, "  - ") {
				out = append(out, "    mode: disable  "+holdMark)
			}
		}
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644)
}

// SetPhase writes the recovery phase into the shared variables (config/vars.yaml; A140), an empty value for running:
// every component's configuration refers to it ($var:RECOVERY_PHASE, BrickKit injects only the keys a component's
// configuration names), so the variable is always there; the rest of the file stays as it is. Every component reads
// it at its next start (be-protocol P1.22).
func (p *Project) SetPhase(phase string) error {
	path := filepath.Join(p.Dir, "config", "vars.yaml")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	s := phaseLine.ReplaceAllString(string(raw), "")
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	value := phase
	if value == "" {
		value = `""`
	}
	s += "RECOVERY_PHASE: " + value + "\n"
	return os.WriteFile(path, []byte(s), 0o644)
}
