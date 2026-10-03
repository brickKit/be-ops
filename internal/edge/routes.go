// Package edge turns the edge_routes of every installed component into the deploy-entry fields
// brickKit reads (foundations 18, bk7): Traefik router labels on Docker and Podman (bounded by
// path segment, Traefik ≥ 3.2), expose / hostname / tlsSecret / paths on Kubernetes; a shell's
// entry also carries the routers of its members. It owns exactly those fields and the traefik.*
// label keys. Gate edge-routes-fresh runs Check and Coverage.
package edge

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/brickKit/be-ops/internal/component"
)

// DefaultBodyLimit is the edge body limit of a route that states none (foundations 18).
const DefaultBodyLimit = 1 << 20

// Target is what the edge needs of one component version: its ID, main port and routes.
type Target struct {
	ID     string
	Port   int
	Routes []component.EdgeRoute
}

// TargetOf builds the target of a loaded component.
func TargetOf(c *component.Component) Target {
	return Target{ID: c.ID, Port: c.Port, Routes: c.Assembly.EdgeRoutes}
}

// Prefix is the segment-bounded prefix of a route: "/erp/sales/**" -> "/erp/sales"; the
// fallback "/**" -> "" (the whole host); a path without /** is itself the prefix.
func Prefix(r component.EdgeRoute) string {
	p := strings.TrimSuffix(r.Path, "/**")
	if r.Path == "/**" {
		return ""
	}
	return strings.TrimSuffix(p, "/")
}

// name is the unversioned router and service name: <scope>-<name>.
func name(id string) string { return strings.ReplaceAll(id, "/", "-") }

// ValidateTargets refuses malformed paths and two components claiming one prefix.
func ValidateTargets(ts map[string]Target) error {
	owner := map[string]string{}
	var errs []string
	for _, id := range sortedIDs(ts) {
		t := ts[id]
		for _, r := range t.Routes {
			if !strings.HasPrefix(r.Path, "/") || strings.Contains(strings.TrimSuffix(r.Path, "/**"), "*") {
				errs = append(errs, fmt.Sprintf("%s: edge route %q is not /prefix/** or a path", t.ID, r.Path))
				continue
			}
			p := Prefix(r)
			if prev, ok := owner[p]; ok && prev != t.ID {
				errs = append(errs, fmt.Sprintf("edge prefix %q is claimed by both %s and %s", r.Path, prev, t.ID))
			}
			owner[p] = t.ID
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

func sortedIDs(ts map[string]Target) []string {
	ids := make([]string, 0, len(ts))
	for id := range ts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Labels renders the Traefik labels of one component: one router per route, its service on the
// component's main port, the shared be-edge chain and the route's own limits.
func Labels(t Target, s Settings) (map[string]string, error) {
	if len(t.Routes) == 0 {
		return nil, nil
	}
	if t.Port == 0 {
		return nil, fmt.Errorf("%s: no deployment.port", t.ID)
	}
	svc := name(t.ID)
	l := map[string]string{
		"traefik.enable": "true",
		"traefik.http.services." + svc + ".loadbalancer.server.port": strconv.Itoa(t.Port),
	}
	for i, r := range t.Routes {
		rn := fmt.Sprintf("%s-%d", svc, i)
		router := "traefik.http.routers." + rn
		p := Prefix(r)
		l[router+".rule"] = rule(s.Host, p)
		l[router+".priority"] = strconv.Itoa(max(len(p), 1))
		l[router+".service"] = svc
		if len(s.EntryPoints) > 0 {
			l[router+".entrypoints"] = strings.Join(s.EntryPoints, ",")
		}
		mws := []string{"be-edge@file", rn + "-limits"}
		body := r.BodyLimit
		if body == 0 {
			body = DefaultBodyLimit
		}
		l["traefik.http.middlewares."+rn+"-limits.buffering.maxRequestBodyBytes"] = strconv.FormatInt(body, 10)
		if r.Rate != nil {
			mws = append(mws, rn+"-rate")
			l["traefik.http.middlewares."+rn+"-rate.ratelimit.average"] = strconv.Itoa(r.Rate.Average)
			l["traefik.http.middlewares."+rn+"-rate.ratelimit.burst"] = strconv.Itoa(r.Rate.Burst)
		}
		l[router+".middlewares"] = strings.Join(mws, ",")
	}
	return l, nil
}

// rule bounds the prefix by a path segment: PathPrefix compares strings, so /erp/sales would
// also take /erp/salesman.
func rule(host, prefix string) string {
	var parts []string
	if host != "" {
		parts = append(parts, "Host(`"+host+"`)")
	}
	if prefix != "" {
		parts = append(parts, "PathRegexp(`^"+regexp.QuoteMeta(prefix)+"(/|$)`)")
	}
	if len(parts) == 0 {
		return "PathPrefix(`/`)"
	}
	return strings.Join(parts, " && ")
}

// Paths are the Kubernetes paths of a component: its prefixes; nil for the whole host.
func Paths(t Target) []string {
	var out []string
	for _, r := range t.Routes {
		if p := Prefix(r); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Coverage reports every OpenAPI operation of a component that lies under none of its
// prefixes. Operations marked x-be-internal: true or tagged provider are system traffic and
// never routed, so they are exempt.
func Coverage(c *component.Component) ([]string, error) {
	ops, err := c.OpenAPIOperations()
	if err != nil {
		return nil, err
	}
	t := TargetOf(c)
	var out []string
	for _, o := range ops {
		if o.Internal || covered(o.Path, t.Routes) {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s %s (%s) lies under no edge_routes prefix", c.ID, strings.ToUpper(o.Method), o.Path, o.File))
	}
	return out, nil
}

func covered(path string, routes []component.EdgeRoute) bool {
	for _, r := range routes {
		p := Prefix(r)
		if r.Path == "/**" || path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
