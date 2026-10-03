package edge

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Check compares the fields be-ops owns in a deploy file with what Apply would write. It returns
// one line per entry that differs; none means current.
func Check(src []byte, ts map[string]Target, s Settings) ([]string, error) {
	want, err := Apply(src, ts, s)
	if err != nil {
		return nil, err
	}
	have, err := ownedFields(src)
	if err != nil {
		return nil, err
	}
	gen, err := ownedFields(want)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for k := range have {
		keys[k] = true
	}
	for k := range gen {
		keys[k] = true
	}
	var out []string
	for _, k := range sortedKeys(keys) {
		if d := fieldDiff(have[k], gen[k]); d != "" {
			out = append(out, k+": "+d)
		}
	}
	return out, nil
}

// fieldDiff names the generated fields of one entry that are missing, extra or different.
func fieldDiff(have, want any) string {
	h, _ := have.(map[string]any)
	w, _ := want.(map[string]any)
	if h == nil && w == nil {
		if reflect.DeepEqual(have, want) {
			return ""
		}
		return fmt.Sprintf("have %v, want %v", have, want)
	}
	var missing, extra, differ []string
	for k, v := range w {
		hv, ok := h[k]
		switch {
		case !ok:
			missing = append(missing, k)
		case !reflect.DeepEqual(hv, v):
			differ = append(differ, fmt.Sprintf("%s (have %v, want %v)", k, hv, v))
		}
	}
	for k := range h {
		if _, ok := w[k]; !ok {
			extra = append(extra, k)
		}
	}
	var parts []string
	for _, p := range []struct {
		label string
		keys  []string
	}{{"missing", missing}, {"not generated", extra}, {"different", differ}} {
		if len(p.keys) > 0 {
			sort.Strings(p.keys)
			parts = append(parts, fmt.Sprintf("%d %s: %s", len(p.keys), p.label, strings.Join(p.keys, ", ")))
		}
	}
	return strings.Join(parts, "; ")
}

// ownedFields extracts, per entry path (shell/member), the traefik.* labels and the Kubernetes
// fields, plus k8s.ingressAnnotations.
func ownedFields(src []byte) (map[string]any, error) {
	var doc struct {
		K8s struct {
			IngressAnnotations map[string]string `yaml:"ingressAnnotations"`
		} `yaml:"k8s"`
		Components []deployEntry `yaml:"components"`
	}
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if len(doc.K8s.IngressAnnotations) > 0 {
		out["k8s.ingressAnnotations"] = doc.K8s.IngressAnnotations
	}
	var walk func(prefix string, es []deployEntry)
	walk = func(prefix string, es []deployEntry) {
		for _, e := range es {
			id := prefix + e.ID
			f := map[string]any{}
			for k, v := range e.Labels {
				if strings.HasPrefix(k, "traefik.") {
					f[k] = v
				}
			}
			if e.Expose {
				f["expose"] = true
			}
			if e.Hostname != "" {
				f["hostname"] = e.Hostname
			}
			if e.TLSSecret != "" {
				f["tlsSecret"] = e.TLSSecret
			}
			if len(e.Paths) > 0 {
				f["paths"] = strings.Join(e.Paths, ",")
			}
			if len(f) > 0 {
				out[id] = f
			}
			walk(id+" > ", e.Members)
		}
	}
	walk("", doc.Components)
	return out, nil
}

type deployEntry struct {
	ID        string            `yaml:"id"`
	Labels    map[string]string `yaml:"labels"`
	Expose    bool              `yaml:"expose"`
	Hostname  string            `yaml:"hostname"`
	TLSSecret string            `yaml:"tlsSecret"`
	Paths     []string          `yaml:"paths"`
	Members   []deployEntry     `yaml:"members"`
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
