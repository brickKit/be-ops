package edge

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// owned are the entry fields be-ops writes on Kubernetes.
var owned = []string{"expose", "hostname", "tlsSecret", "paths"}

// Apply writes the generated fields into a deploy file. Targets are keyed by the entry id as
// written (erp/sales or erp/sales@2.0.0; a versioned entry falls back to the bare ID).
//
// Docker and Podman: every running entry with routes gets its routers as traefik.* labels; a
// shell entry also gets the routers of its running members (a member's own labels do not apply
// while it is hosted). Kubernetes: expose, hostname, tlsSecret and paths on every running entry
// with routes, members included (the shell exposes them); traefik.* labels are removed. An entry
// with mode disable, local or debug, or without routes, has none of these fields.
func Apply(src []byte, ts map[string]Target, s Settings) ([]byte, error) {
	if err := ValidateTargets(ts); err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	k8s := scalar(get(root, "target")) == "k8s"
	if k8s && s.Host == "" {
		return nil, fmt.Errorf("target k8s: the edge settings need a host (the Ingress hostname)")
	}
	comps := get(root, "components")
	if comps != nil {
		for _, e := range comps.Content {
			if err := applyEntry(e, ts, s, k8s); err != nil {
				return nil, err
			}
		}
	}
	if k8s && len(s.IngressAnnotations) > 0 {
		k := get(root, "k8s")
		if k == nil {
			k = &yaml.Node{Kind: yaml.MappingNode}
			set(root, "k8s", k)
		}
		set(k, "ingressAnnotations", stringMap(s.IngressAnnotations))
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func applyEntry(e *yaml.Node, ts map[string]Target, s Settings, k8s bool) error {
	t, found := find(e, ts)
	runs := running(e) && found && len(t.Routes) > 0
	own := map[string]string{}
	if runs {
		l, err := Labels(t, s)
		if err != nil {
			return err
		}
		own = l
	}
	shellLabels := map[string]string{}
	if ms := get(e, "members"); ms != nil {
		for _, m := range ms.Content {
			if err := applyEntry(m, ts, s, k8s); err != nil {
				return err
			}
			if mt, ok := find(m, ts); ok && running(e) && running(m) {
				l, err := Labels(mt, s)
				if err != nil {
					return err
				}
				for k, v := range l {
					shellLabels[k] = v
				}
			}
		}
	}
	if k8s {
		setLabels(e, nil)
		setK8s(e, t, runs && len(t.Routes) > 0, s)
		return nil
	}
	for k, v := range shellLabels {
		own[k] = v
	}
	setLabels(e, own)
	return nil
}

// find looks an entry's target up by its id, a versioned id falling back to the bare ID.
func find(e *yaml.Node, ts map[string]Target) (Target, bool) {
	id := scalar(get(e, "id"))
	t, ok := ts[id]
	if !ok {
		bare, _, _ := strings.Cut(id, "@")
		t, ok = ts[bare]
	}
	return t, ok
}

// running reports whether an entry runs as a container the edge can reach.
func running(e *yaml.Node) bool {
	switch scalar(get(e, "mode")) {
	case "disable", "local", "debug":
		return false
	}
	return true
}

func setK8s(e *yaml.Node, t Target, on bool, s Settings) {
	for _, f := range owned {
		del(e, f)
	}
	if !on {
		return
	}
	set(e, "expose", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	set(e, "hostname", str(s.Host))
	if s.TLSSecret != "" {
		set(e, "tlsSecret", str(s.TLSSecret))
	}
	if ps := Paths(t); len(ps) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, p := range ps {
			seq.Content = append(seq.Content, str(p))
		}
		set(e, "paths", seq)
	}
}

// setLabels replaces the traefik.* keys of an entry's labels, keeping every other label.
func setLabels(e *yaml.Node, gen map[string]string) {
	l := get(e, "labels")
	if l == nil {
		if len(gen) == 0 {
			return
		}
		l = &yaml.Node{Kind: yaml.MappingNode}
		set(e, "labels", l)
	}
	var kept []*yaml.Node
	for i := 0; i+1 < len(l.Content); i += 2 {
		if !strings.HasPrefix(l.Content[i].Value, "traefik.") {
			kept = append(kept, l.Content[i], l.Content[i+1])
		}
	}
	l.Content = kept
	keys := make([]string, 0, len(gen))
	for k := range gen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		l.Content = append(l.Content, str(k), str(gen[k]))
	}
	if len(l.Content) == 0 {
		del(e, "labels")
	}
}

func stringMap(m map[string]string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n.Content = append(n.Content, str(k), str(m[k]))
	}
	return n
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func scalar(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func set(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, str(key), v)
}

func del(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
