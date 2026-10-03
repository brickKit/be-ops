package protocol

import (
	"encoding/json"
	"fmt"
	"regexp"

	beprotocol "github.com/brickKit/be-protocol"
)

// AssemblySchema is what be-ops takes from schemas/assembly-protocol.schema.json.
type AssemblySchema struct {
	Capabilities []string
	ResourceType *regexp.Regexp
	PermKey      *regexp.Regexp
}

// LoadAssemblySchema reads the capability enum and the name patterns of the assembly schema.
func LoadAssemblySchema() (*AssemblySchema, error) {
	raw, err := beprotocol.FS.ReadFile("schemas/assembly-protocol.schema.json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Defs struct {
			PermKey    struct{ Pattern string } `json:"permKey"`
			Capability struct{ Enum []string }  `json:"capability"`
			Resource   struct {
				Properties struct {
					Type struct{ Pattern string } `json:"type"`
				} `json:"properties"`
			} `json:"resource"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("assembly-protocol.schema.json: %w", err)
	}
	s := &AssemblySchema{Capabilities: doc.Defs.Capability.Enum}
	if s.ResourceType, err = regexp.Compile(doc.Defs.Resource.Properties.Type.Pattern); err != nil {
		return nil, err
	}
	if s.PermKey, err = regexp.Compile(doc.Defs.PermKey.Pattern); err != nil {
		return nil, err
	}
	if len(s.Capabilities) == 0 {
		return nil, fmt.Errorf("assembly-protocol.schema.json: no capability enum")
	}
	return s, nil
}

// IsCapability reports whether a name is an authorization-provider capability.
func (s *AssemblySchema) IsCapability(c string) bool {
	for _, x := range s.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}
