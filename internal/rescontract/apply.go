package rescontract

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/yamlblock"
	"gopkg.in/yaml.v3"
)

type targetFile struct {
	path   string
	prefix string // what the paths are written relative to: "" when servers is /<id>, else /<id>
	src    []byte
}

// target picks the published OpenAPI file the contract is merged into: the one contracts/*.openapi.yaml
// whose servers URL is /<id>; failing that, the only OpenAPI file when it has no servers URL
// (its paths are then written in full). nil when the component declares no resources and has no
// file to clean.
func target(c *component.Component) (*targetFile, error) {
	files, err := filepath.Glob(filepath.Join(c.Dir, "contracts", "*.openapi.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var bare []*targetFile
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Servers []struct {
				URL string `yaml:"url"`
			} `yaml:"servers"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		switch {
		case len(doc.Servers) > 0 && strings.TrimSuffix(doc.Servers[0].URL, "/") == "/"+c.ID:
			return &targetFile{path: f, src: raw}, nil
		case len(doc.Servers) == 0 || doc.Servers[0].URL == "" || doc.Servers[0].URL == "/":
			bare = append(bare, &targetFile{path: f, prefix: "/" + c.ID, src: raw})
		}
	}
	if len(files) == 1 && len(bare) == 1 {
		return bare[0], nil
	}
	if len(c.Assembly.Resources) == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("%s declares resources but no contracts/*.openapi.yaml has servers url /%s to merge the resource contract into", c.ID, c.ID)
}

// Apply returns the target file with the region rewritten, and its path. Generated paths that a
// hand-written path already uses are an error.
func Apply(c *component.Component) ([]byte, string, error) {
	t, err := target(c)
	if err != nil || t == nil {
		return nil, "", err
	}
	region, err := generate(c, t.prefix)
	if err != nil {
		return nil, "", err
	}
	if err := collisions(t, region); err != nil {
		return nil, "", err
	}
	out, err := yamlblock.ReplaceRegion(t.src, "paths", Begin, End, region)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", t.path, err)
	}
	return out, t.path, nil
}

// Check reports a target file whose region is not what Apply would write.
func Check(c *component.Component) ([]string, error) {
	out, path, err := Apply(c)
	if err != nil || path == "" {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if string(raw) != string(out) {
		return []string{fmt.Sprintf("%s: %s: the resource contract region is not current: run be-ops openapi", c.ID, path)}, nil
	}
	return nil, nil
}

func collisions(t *targetFile, region string) error {
	hand, err := yamlblock.ReplaceRegion(t.src, "paths", Begin, End, "")
	if err != nil {
		return fmt.Errorf("%s: %w", t.path, err)
	}
	var doc struct {
		Paths map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(hand, &doc); err != nil {
		return fmt.Errorf("%s: %w", t.path, err)
	}
	var gen map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(strings.ReplaceAll("\n"+region, "\n  ", "\n")), &gen); err != nil {
		return err
	}
	var clash []string
	for p := range gen {
		if _, ok := doc.Paths[p]; ok {
			clash = append(clash, p)
		}
	}
	if len(clash) > 0 {
		sort.Strings(clash)
		return fmt.Errorf("%s: hand-written paths %s belong to the resource contract (P6.10): remove them", t.path, strings.Join(clash, ", "))
	}
	return nil
}
