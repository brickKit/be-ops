// Package protocol reads the machine-readable parts of be-protocol that be-ops generates from,
// pinned through the Go module github.com/brickKit/be-protocol (go.mod states the tag). Nothing
// here is copied: the catalogue is read from the module's embedded files at run time.
package protocol

import (
	"fmt"
	"regexp"
	"strings"

	beprotocol "github.com/brickKit/be-protocol"
	"gopkg.in/yaml.v3"
)

// SupportedVersion is the protocol MAJOR.MINOR this be-ops generates for.
const SupportedVersion = "1.0"

// Key is one protocol configuration key of schemas/config-keys.yaml (be-protocol P2).
type Key struct {
	Name         string   `yaml:"name"`
	Type         string   `yaml:"type"`
	Format       string   `yaml:"format"`
	Required     bool     `yaml:"required"`
	Default      *string  `yaml:"default"`
	ShellDefault *string  `yaml:"shell_default"`
	DefaultFrom  string   `yaml:"default_from"`
	OneOf        []string `yaml:"one_of"`
	Enum         []string `yaml:"enum"`
	Secret       bool     `yaml:"secret"`
	Mount        string   `yaml:"mount"`
	Shared       bool     `yaml:"shared"`
	Profiles     []string `yaml:"profiles"`
	AppliesWhen  string   `yaml:"applies_when"`
	Ref          string   `yaml:"ref"`
}

// InProfile reports whether the key belongs to the named profile.
func (k Key) InProfile(p string) bool {
	for _, x := range k.Profiles {
		if x == p {
			return true
		}
	}
	return false
}

// Pattern is a family of component-defined keys the protocol gives a meaning (…_NO_FORMAT).
type Pattern struct {
	Pattern string `yaml:"pattern"`
	Type    string `yaml:"type"`
	re      *regexp.Regexp
}

// Retired is a key that must no longer be declared.
type Retired struct {
	Name       string `yaml:"name"`
	ReplacedBy string `yaml:"replaced_by"`
	Note       string `yaml:"note"`
}

// Secrets states how a secret key is declared (P2.12).
type Secrets struct {
	Mount  string `yaml:"mount"`
	Suffix string `yaml:"suffix"`
	Root   string `yaml:"root"`
}

// Catalogue is schemas/config-keys.yaml.
type Catalogue struct {
	Protocol string    `yaml:"protocol"`
	Keys     []Key     `yaml:"keys"`
	Patterns []Pattern `yaml:"patterns"`
	Reserved struct {
		Exact  []string `yaml:"exact"`
		Suffix []string `yaml:"suffix"`
	} `yaml:"reserved"`
	Secrets Secrets   `yaml:"secrets"`
	Retired []Retired `yaml:"retired"`
}

// LoadCatalogue reads schemas/config-keys.yaml from the pinned be-protocol module.
func LoadCatalogue() (*Catalogue, error) {
	raw, err := beprotocol.FS.ReadFile("schemas/config-keys.yaml")
	if err != nil {
		return nil, err
	}
	return ParseCatalogue(raw)
}

// ParseCatalogue parses a config-keys.yaml document.
func ParseCatalogue(raw []byte) (*Catalogue, error) {
	var c Catalogue
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("config-keys.yaml: %w", err)
	}
	if c.Protocol != SupportedVersion {
		return nil, fmt.Errorf("config-keys.yaml: protocol %q, be-ops supports %s", c.Protocol, SupportedVersion)
	}
	for i := range c.Patterns {
		re, err := regexp.Compile(c.Patterns[i].Pattern)
		if err != nil {
			return nil, fmt.Errorf("config-keys.yaml pattern %q: %w", c.Patterns[i].Pattern, err)
		}
		c.Patterns[i].re = re
	}
	return &c, nil
}

// Key looks a protocol key up by name.
func (c *Catalogue) Key(name string) (Key, bool) {
	for _, k := range c.Keys {
		if k.Name == name {
			return k, true
		}
	}
	return Key{}, false
}

// RetiredKey looks a retired key up by name.
func (c *Catalogue) RetiredKey(name string) (Retired, bool) {
	for _, r := range c.Retired {
		if r.Name == name {
			return r, true
		}
	}
	return Retired{}, false
}

// IsReserved reports whether a name belongs to the platform (P2.4).
func (c *Catalogue) IsReserved(name string) bool {
	for _, e := range c.Reserved.Exact {
		if name == e {
			return true
		}
	}
	for _, s := range c.Reserved.Suffix {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// MatchesPattern reports whether a component-defined key falls under a protocol pattern.
func (c *Catalogue) MatchesPattern(name string) bool {
	_, ok := c.PatternFor(name)
	return ok
}

// PatternFor returns the protocol pattern a component-defined key falls under.
func (c *Catalogue) PatternFor(name string) (Pattern, bool) {
	for _, p := range c.Patterns {
		if p.re.MatchString(name) {
			return p, true
		}
	}
	return Pattern{}, false
}
