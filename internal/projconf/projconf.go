// Package projconf reads a brickKit project the way be-ops needs it: the installed components
// (brickkit.yaml) and the configuration values of each (config/<scope>-<name>.yaml, $var:
// references through config/vars.yaml and a deploy file's vars:). Database role and schema names
// come from here, never from a naming convention.
package projconf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Installed is one brickkit.yaml component entry.
type Installed struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
}

// Project is a loaded project.
type Project struct {
	Root       string
	Components []Installed
	vars       map[string]string
	configs    map[string]map[string]string
}

// Kind is the form of a configuration value.
type Kind int

const (
	Absent  Kind = iota
	Literal      // a literal, or a $var: reference resolved to one
	Env          // ${NAME}: Value is NAME
	File         // file://path: Value is the path
)

// Value is a resolved configuration value.
type Value struct {
	Kind  Kind
	Value string
}

// Load reads brickkit.yaml, config/vars.yaml and, when deploy is not empty, that deploy file's
// vars: (which win over config/vars.yaml, as brickKit resolves them).
func Load(root, deploy string) (*Project, error) {
	p := &Project{Root: root, vars: map[string]string{}, configs: map[string]map[string]string{}}
	var bk struct {
		Components []Installed `yaml:"components"`
	}
	if err := readYAML(filepath.Join(root, "brickkit.yaml"), &bk, false); err != nil {
		return nil, err
	}
	p.Components = bk.Components
	if err := readYAML(filepath.Join(root, "config", "vars.yaml"), &p.vars, true); err != nil {
		return nil, err
	}
	if deploy != "" {
		var d struct {
			Vars map[string]string `yaml:"vars"`
		}
		if err := readYAML(deploy, &d, false); err != nil {
			return nil, err
		}
		for k, v := range d.Vars {
			p.vars[k] = v
		}
	}
	return p, nil
}

func readYAML(path string, out any, optional bool) error {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) && optional {
		return nil
	}
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// ConfigPath is the configuration file of a component: config/<scope>-<name>.yaml.
func (p *Project) ConfigPath(id string) string {
	return filepath.Join(p.Root, "config", strings.ReplaceAll(id, "/", "-")+".yaml")
}

// Resolve returns the value of a component's key. A $var: reference is resolved; ${NAME} and
// file:// are returned as their kind (be-ops never reads a secret value).
func (p *Project) Resolve(id, key string) (Value, error) {
	cfg, ok := p.configs[id]
	if !ok {
		cfg = map[string]string{}
		if err := readYAML(p.ConfigPath(id), &cfg, true); err != nil {
			return Value{}, err
		}
		p.configs[id] = cfg
	}
	raw, ok := cfg[key]
	if !ok {
		return Value{Kind: Absent}, nil
	}
	return p.resolve(id, key, raw, 0)
}

func (p *Project) resolve(id, key, raw string, depth int) (Value, error) {
	switch {
	case strings.HasPrefix(raw, "$var:"):
		name := strings.TrimPrefix(raw, "$var:")
		v, ok := p.vars[name]
		if !ok {
			return Value{}, fmt.Errorf("%s: %s refers to $var:%s, which config/vars.yaml does not define", p.ConfigPath(id), key, name)
		}
		if depth > 0 {
			return Value{}, fmt.Errorf("%s: %s: a shared variable may not refer to another", p.ConfigPath(id), key)
		}
		return p.resolve(id, key, v, depth+1)
	case strings.HasPrefix(raw, "${") && strings.HasSuffix(raw, "}"):
		return Value{Kind: Env, Value: raw[2 : len(raw)-1]}, nil
	case strings.HasPrefix(raw, "file://"):
		return Value{Kind: File, Value: strings.TrimPrefix(raw, "file://")}, nil
	}
	return Value{Kind: Literal, Value: raw}, nil
}
