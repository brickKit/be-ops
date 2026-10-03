package authzdecl

import (
	"encoding/json"
	"sort"

	"github.com/brickKit/be-ops/internal/component"
)

// CatalogType is one resource_types entry of the RESOURCE_CATALOG file (contract-infra-authz
// schemas/catalog.schema.json, $defs.resource_type).
type CatalogType struct {
	Type           string                        `json:"type"`
	OwnerComponent string                        `json:"owner_component"`
	Table          string                        `json:"table"`
	ViewKey        string                        `json:"view_key"`
	Keys           []string                      `json:"keys"`
	Dimensions     []string                      `json:"dimensions,omitempty"`
	Relations      map[string]component.Relation `json:"relations"`
	Share          *component.Share              `json:"share,omitempty"`
	Fields         []component.FieldSet          `json:"fields,omitempty"`
	Inherits       []component.Inherit           `json:"inherits,omitempty"`
	Derivation     string                        `json:"derivation"`
}

// CatalogTypes lists the resource types of the components, sorted by type.
func CatalogTypes(cs []*component.Component) []CatalogType {
	var out []CatalogType
	for _, c := range cs {
		for _, r := range c.Assembly.Resources {
			ct := CatalogType{Type: r.Type, OwnerComponent: c.ID, Table: r.Table, ViewKey: r.ViewKey, Keys: r.Keys,
				Dimensions: r.Dimensions, Relations: r.Relations, Share: r.Share, Fields: r.Fields,
				Inherits: r.Inherits, Derivation: r.Derivation}
			if ct.Relations == nil {
				ct.Relations = map[string]component.Relation{}
			}
			if ct.Derivation == "" {
				ct.Derivation = "direct"
			}
			out = append(out, ct)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// Catalog renders the RESOURCE_CATALOG file: {"resource_types": [...]}, indented, deterministic.
func Catalog(cs []*component.Component) ([]byte, error) {
	types := CatalogTypes(cs)
	if types == nil {
		types = []CatalogType{}
	}
	b, err := json.MarshalIndent(struct {
		ResourceTypes []CatalogType `json:"resource_types"`
	}{types}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
