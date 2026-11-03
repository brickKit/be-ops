// Package features makes the deployment's features document (protocol 20, UI25): the components the project
// selected and their exact versions, from its brickkit.yaml. The edge serves the file to live sessions only
// (be-protocol P11.8); it goes through neither the identity nor the authorization member and does not vary by user.
package features

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// Component is one selected component.
type Component struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Document is the features document.
type Document struct {
	Project    string      `json:"project"`
	Components []Component `json:"components"`
}

// FromManifest reads a brickkit.yaml.
func FromManifest(raw []byte) (Document, error) {
	var m struct {
		Project    string      `yaml:"project"`
		Components []Component `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return Document{}, fmt.Errorf("brickkit.yaml: %w", err)
	}
	seen := map[string]bool{}
	for _, c := range m.Components {
		if c.ID == "" || c.Version == "" {
			return Document{}, fmt.Errorf("brickkit.yaml: a component without id or version: %+v", c)
		}
		if seen[c.ID] {
			return Document{}, fmt.Errorf("brickkit.yaml: %s is listed twice", c.ID)
		}
		seen[c.ID] = true
	}
	sort.Slice(m.Components, func(i, j int) bool { return m.Components[i].ID < m.Components[j].ID })
	if m.Components == nil {
		m.Components = []Component{}
	}
	return Document{Project: m.Project, Components: m.Components}, nil
}

// Write writes the document, indented, to path.
func Write(path string, d Document) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
