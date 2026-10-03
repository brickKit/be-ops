package component

import "gopkg.in/yaml.v3"

// EdgeRoute is one assembly.yaml edge_routes entry (foundations 18, "Declaration").
type EdgeRoute struct {
	Path      string `yaml:"path"`
	Auth      string `yaml:"auth"`
	BodyLimit int64  `yaml:"body_limit"`
	Timeout   int    `yaml:"timeout"`
	Rate      *Rate  `yaml:"rate"`
}

// Rate is a per-route edge rate limit: requests per second per client IP, and burst.
type Rate struct {
	Average int `yaml:"average"`
	Burst   int `yaml:"burst"`
}

// Permission is one assembly.yaml permissions entry.
type Permission struct {
	Key       string `yaml:"key"`
	Title     string `yaml:"title"`
	Type      string `yaml:"type"`
	Delegable *bool  `yaml:"delegable"`
}

// Relation is one relation of a resource type.
type Relation struct {
	Grants   []string `yaml:"grants" json:"grants,omitempty"`
	Includes []string `yaml:"includes" json:"includes,omitempty"`
	OwnedBy  string   `yaml:"owned_by" json:"owned_by,omitempty"`
}

// Share is the share rule of a resource type.
type Share struct {
	Key       string   `yaml:"key" json:"key"`
	Relations []string `yaml:"relations" json:"relations"`
	Subjects  []string `yaml:"subjects" json:"subjects"`
}

// FieldSet is one field set of a resource type.
type FieldSet struct {
	Set     string   `yaml:"set" json:"set"`
	Columns []string `yaml:"columns" json:"columns"`
	Read    string   `yaml:"read" json:"read"`
	Edit    string   `yaml:"edit" json:"edit,omitempty"`
}

// Inherit is a one-hop derivation from a parent type.
type Inherit struct {
	From     string `yaml:"from" json:"from"`
	Via      string `yaml:"via" json:"via"`
	Relation string `yaml:"relation" json:"relation"`
	As       string `yaml:"as" json:"as"`
}

// Resource is one assembly.yaml resources entry (be-protocol assembly-protocol.schema.json).
type Resource struct {
	Type       string              `yaml:"type"`
	Table      string              `yaml:"table"`
	ViewKey    string              `yaml:"view_key"`
	Keys       []string            `yaml:"keys"`
	Dimensions []string            `yaml:"dimensions"`
	Relations  map[string]Relation `yaml:"relations"`
	Share      *Share              `yaml:"share"`
	Fields     []FieldSet          `yaml:"fields"`
	Inherits   []Inherit           `yaml:"inherits"`
	Derivation string              `yaml:"derivation"`
}

// DataScope is one assembly.yaml data_scopes entry.
type DataScope struct {
	Dimension string   `yaml:"dimension"`
	Column    string   `yaml:"column"`
	Mode      string   `yaml:"mode"`
	Tables    []string `yaml:"tables"`
}

// Assembly is the part of assembly.yaml be-ops reads.
type Assembly struct {
	ID          string `yaml:"id"`
	Protocol    string `yaml:"protocol"`
	Conformance struct {
		Fixtures string `yaml:"fixtures"`
	} `yaml:"conformance"`
	EdgeRoutes           []EdgeRoute  `yaml:"edge_routes"`
	Permissions          []Permission `yaml:"permissions"`
	Resources            []Resource   `yaml:"resources"`
	RequiresCapabilities []string     `yaml:"requires_capabilities"`
	DataScopesNode       yaml.Node    `yaml:"data_scopes"`
}

// Permission looks an own permission key up.
func (a Assembly) Permission(key string) (Permission, bool) {
	for _, p := range a.Permissions {
		if p.Key == key {
			return p, true
		}
	}
	return Permission{}, false
}

// DataScopes returns the declared data scopes; nil for `none` or absent.
func (a Assembly) DataScopes() []DataScope {
	if a.DataScopesNode.Kind != yaml.SequenceNode {
		return nil
	}
	var out []DataScope
	_ = a.DataScopesNode.Decode(&out)
	return out
}
