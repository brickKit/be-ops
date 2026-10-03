package component

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// EventEntry is one entry of an event contract's `events` list.
type EventEntry struct {
	Subject string
	// Signal is true for a best-effort poke (P12.10) listed among the events: marked
	// `x-signal: true` or carrying a `transport`. Pokes are never declared (P12.16).
	Signal bool
	File   string
}

// EventContracts reads every contracts/events/*.events.json, files in name order, entries in
// file order. Top-level signal lists (`signals`, `x-signals`) and inbound lists are not events
// this component publishes and are not returned.
func (c *Component) EventContracts() ([]EventEntry, error) {
	files, err := filepath.Glob(filepath.Join(c.Dir, "contracts", "events", "*.events.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []EventEntry
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Events []struct {
				Subject   string `json:"subject"`
				Signal    bool   `json:"x-signal"`
				Transport string `json:"transport"`
			} `json:"events"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, e := range doc.Events {
			out = append(out, EventEntry{Subject: e.Subject, Signal: e.Signal || e.Transport != "", File: f})
		}
	}
	return out, nil
}

// FixtureEvents is what be-ops reads from conformance/fixtures.yaml.
type FixtureEvents struct {
	Present  bool
	Consumes []string
}

// Fixtures reads events.consumes of the conformance fixtures file.
func (c *Component) Fixtures() (FixtureEvents, error) {
	raw, err := os.ReadFile(c.FixturesPath())
	if os.IsNotExist(err) {
		return FixtureEvents{}, nil
	}
	if err != nil {
		return FixtureEvents{}, err
	}
	var doc struct {
		Events struct {
			Consumes []struct {
				Subject string `yaml:"subject"`
			} `yaml:"consumes"`
		} `yaml:"events"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return FixtureEvents{}, fmt.Errorf("%s: %w", c.FixturesPath(), err)
	}
	fx := FixtureEvents{Present: true}
	for _, e := range doc.Events.Consumes {
		fx.Consumes = append(fx.Consumes, e.Subject)
	}
	return fx, nil
}

// Operation is one OpenAPI operation with its full path (server prefix + path).
type Operation struct {
	File            string
	Path            string
	Method          string
	Permission      string // x-be-permission; "" when absent
	DeadlineSeconds int    // x-be-deadline-seconds; 0 when absent
	Tags            []string
	Internal        bool // x-be-internal: true, or tagged provider: never routed through the edge
}

var httpMethods = []string{"get", "put", "post", "delete", "patch", "head", "options"}

// OpenAPIOperations reads every contracts/*.openapi.yaml.
func (c *Component) OpenAPIOperations() ([]Operation, error) {
	files, err := filepath.Glob(filepath.Join(c.Dir, "contracts", "*.openapi.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []Operation
	for _, f := range files {
		ops, err := readOpenAPI(f)
		if err != nil {
			return nil, err
		}
		out = append(out, ops...)
	}
	return out, nil
}

type openapiOp struct {
	Permission string   `yaml:"x-be-permission"`
	Deadline   int      `yaml:"x-be-deadline-seconds"`
	Internal   bool     `yaml:"x-be-internal"`
	Tags       []string `yaml:"tags"`
}

func readOpenAPI(f string) ([]Operation, error) {
	raw, err := os.ReadFile(f)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Servers []struct {
			URL string `yaml:"url"`
		} `yaml:"servers"`
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", f, err)
	}
	prefix := ""
	if len(doc.Servers) > 0 && strings.HasPrefix(doc.Servers[0].URL, "/") {
		prefix = strings.TrimSuffix(doc.Servers[0].URL, "/")
	}
	paths := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []Operation
	for _, p := range paths {
		item := doc.Paths[p]
		for _, m := range httpMethods {
			n, ok := item[m]
			if !ok {
				continue
			}
			var op openapiOp
			if err := n.Decode(&op); err != nil {
				return nil, fmt.Errorf("%s %s %s: %w", f, m, p, err)
			}
			internal := op.Internal
			for _, t := range op.Tags {
				internal = internal || t == "provider"
			}
			out = append(out, Operation{File: f, Path: prefix + p, Method: m, Permission: op.Permission,
				DeadlineSeconds: op.Deadline, Tags: op.Tags, Internal: internal})
		}
	}
	return out, nil
}
