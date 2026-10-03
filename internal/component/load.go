// Package component reads one component directory the way be-ops needs it: the parts of
// component.yaml it generates or checks (configSchema, events, ports, shell), the project keys
// of assembly.yaml (edge_routes, permissions, resources, …), and the contract files beside them
// (event contracts, conformance fixtures, OpenAPI). It never writes.
package component

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Prop is one item of configSchema.properties, reduced to what the protocol block fixes.
type Prop struct {
	Name    string
	Type    string
	Default *string // the scalar text as written; nil when absent
	Secret  bool
	Mount   string
	Node    *yaml.Node // the value node as written (own keys are re-emitted from it)
	KeyNode *yaml.Node
}

// Port is one extra port of deployment.extraPorts.
type Port struct {
	Name     string `yaml:"name"`
	Port     int    `yaml:"port"`
	Protocol string `yaml:"protocol"`
}

// Events is the component.yaml events block.
type Events struct {
	Declared   bool
	Publishes  []string `yaml:"publishes"`
	Subscribes []string `yaml:"subscribes"`
}

// Component is a loaded component directory.
type Component struct {
	Dir          string
	ManifestPath string
	ManifestRaw  []byte
	ID           string
	Version      string
	Port         int
	ExtraPorts   []Port
	StopGrace    int      // deployment.stopGracePeriodSeconds; 0 when absent
	ShellMembers []string // nil unless component.yaml has a shell block
	IsShell      bool
	Props        []Prop
	Required     []string
	Events       Events
	Assembly     Assembly
	HasAssembly  bool
}

type rawManifest struct {
	Metadata struct {
		ID      string `yaml:"id"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
	Shell *struct {
		Members []string `yaml:"members"`
	} `yaml:"shell"`
	ConfigSchema struct {
		Properties yaml.Node `yaml:"properties"`
		Required   []string  `yaml:"required"`
	} `yaml:"configSchema"`
	Deployment struct {
		Port       int    `yaml:"port"`
		ExtraPorts []Port `yaml:"extraPorts"`
		StopGrace  int    `yaml:"stopGracePeriodSeconds"`
	} `yaml:"deployment"`
	Events *Events `yaml:"events"`
}

// Load reads dir/component.yaml and, when present, dir/assembly.yaml.
func Load(dir string) (*Component, error) {
	mp := filepath.Join(dir, "component.yaml")
	raw, err := os.ReadFile(mp)
	if err != nil {
		return nil, err
	}
	var m rawManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", mp, err)
	}
	c := &Component{
		Dir: dir, ManifestPath: mp, ManifestRaw: raw,
		ID: m.Metadata.ID, Version: m.Metadata.Version,
		Port: m.Deployment.Port, ExtraPorts: m.Deployment.ExtraPorts, StopGrace: m.Deployment.StopGrace,
		Required: m.ConfigSchema.Required,
	}
	if m.Shell != nil {
		c.IsShell, c.ShellMembers = true, m.Shell.Members
	}
	if m.Events != nil {
		c.Events = *m.Events
		c.Events.Declared = true
	}
	if c.Props, err = readProps(&m.ConfigSchema.Properties); err != nil {
		return nil, fmt.Errorf("%s configSchema.properties: %w", mp, err)
	}
	ap := filepath.Join(dir, "assembly.yaml")
	if araw, err := os.ReadFile(ap); err == nil {
		if err := yaml.Unmarshal(araw, &c.Assembly); err != nil {
			return nil, fmt.Errorf("%s: %w", ap, err)
		}
		c.HasAssembly = true
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return c, nil
}

func readProps(n *yaml.Node) ([]Prop, error) {
	if n.Kind == 0 {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("not a mapping")
	}
	var out []Prop
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		p := Prop{Name: k.Value, Node: v, KeyNode: k}
		var attrs struct {
			Type    string    `yaml:"type"`
			Default yaml.Node `yaml:"default"`
			Secret  bool      `yaml:"secret"`
			Mount   string    `yaml:"mount"`
		}
		if err := v.Decode(&attrs); err != nil {
			return nil, fmt.Errorf("%s: %w", k.Value, err)
		}
		p.Type, p.Secret, p.Mount = attrs.Type, attrs.Secret, attrs.Mount
		if attrs.Default.Kind == yaml.ScalarNode && attrs.Default.Tag != "!!null" {
			s := attrs.Default.Value
			p.Default = &s
		}
		out = append(out, p)
	}
	return out, nil
}

// Prop looks a configSchema property up by name.
func (c *Component) Prop(name string) (Prop, bool) {
	for _, p := range c.Props {
		if p.Name == name {
			return p, true
		}
	}
	return Prop{}, false
}

// IsRequired reports whether configSchema.required lists the key.
func (c *Component) IsRequired(name string) bool {
	for _, r := range c.Required {
		if r == name {
			return true
		}
	}
	return false
}

// FixturesPath is the conformance fixtures file (assembly.yaml conformance.fixtures, default
// conformance/fixtures.yaml).
func (c *Component) FixturesPath() string {
	rel := c.Assembly.Conformance.Fixtures
	if rel == "" {
		rel = "conformance/fixtures.yaml"
	}
	return filepath.Join(c.Dir, rel)
}

// HasGRPCPort reports whether an extra port is named grpc.
func (c *Component) HasGRPCPort() bool {
	for _, p := range c.ExtraPorts {
		if p.Name == "grpc" {
			return true
		}
	}
	return false
}
