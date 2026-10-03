package edge

import (
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/testfixture"
	"gopkg.in/yaml.v3"
)

func widgetTarget(t *testing.T) Target {
	t.Helper()
	c, err := component.Load(testfixture.Copy(t, "widget"))
	if err != nil {
		t.Fatal(err)
	}
	return TargetOf(c)
}

var settings = Settings{Host: "app.example.com", TLSSecret: "app-tls"}

func TestLabels_WidgetSegmentBoundedRouters(t *testing.T) {
	l, err := Labels(widgetTarget(t), settings)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"traefik.enable": "true",
		"traefik.http.routers.conformance-widget-0.rule":                                     "Host(`app.example.com`) && PathRegexp(`^/conformance/widget/kinds(/|$)`)",
		"traefik.http.routers.conformance-widget-0.priority":                                 "25",
		"traefik.http.routers.conformance-widget-0.service":                                  "conformance-widget",
		"traefik.http.routers.conformance-widget-0.middlewares":                              "be-edge@file,conformance-widget-0-limits",
		"traefik.http.routers.conformance-widget-1.rule":                                     "Host(`app.example.com`) && PathRegexp(`^/conformance/widget(/|$)`)",
		"traefik.http.routers.conformance-widget-1.priority":                                 "19",
		"traefik.http.middlewares.conformance-widget-0-limits.buffering.maxRequestBodyBytes": "1048576",
		"traefik.http.services.conformance-widget.loadbalancer.server.port":                  "8080",
	}
	for k, v := range want {
		if l[k] != v {
			t.Errorf("%s = %q, want %q", k, l[k], v)
		}
	}
}

func TestLabels_FallbackRateAndBodyLimit(t *testing.T) {
	tg := Target{ID: "frontend/standard", Port: 80, Routes: []component.EdgeRoute{{Path: "/**", Auth: "none"}}}
	l, _ := Labels(tg, Settings{})
	if l["traefik.http.routers.frontend-standard-0.rule"] != "PathPrefix(`/`)" || l["traefik.http.routers.frontend-standard-0.priority"] != "1" {
		t.Errorf("fallback = %v", l)
	}
	tg = Target{ID: "infra/iam-casdoor", Port: 8224, Routes: []component.EdgeRoute{{Path: "/api/iam/token", BodyLimit: 4096, Rate: &component.Rate{Average: 10, Burst: 20}}}}
	l, _ = Labels(tg, Settings{})
	if l["traefik.http.routers.infra-iam-casdoor-0.rule"] != "PathRegexp(`^/api/iam/token(/|$)`)" ||
		l["traefik.http.middlewares.infra-iam-casdoor-0-limits.buffering.maxRequestBodyBytes"] != "4096" ||
		l["traefik.http.middlewares.infra-iam-casdoor-0-rate.ratelimit.average"] != "10" ||
		!strings.HasSuffix(l["traefik.http.routers.infra-iam-casdoor-0.middlewares"], ",infra-iam-casdoor-0-rate") {
		t.Errorf("labels = %v", l)
	}
}

func TestValidate_TwoOwnersOfAPrefix(t *testing.T) {
	a := Target{ID: "erp/sales", Port: 1, Routes: []component.EdgeRoute{{Path: "/erp/sales/**"}}}
	b := Target{ID: "erp/salesx", Port: 2, Routes: []component.EdgeRoute{{Path: "/erp/sales/**"}}}
	if err := ValidateTargets(map[string]Target{"erp/sales": a, "erp/salesx": b}); err == nil ||
		!strings.Contains(err.Error(), "erp/sales") || !strings.Contains(err.Error(), "erp/salesx") {
		t.Errorf("want both owners named, got %v", err)
	}
}

const dockerDeploy = `target: docker # docker | podman | k8s
network: proj-net

components:
  - id: conformance/widget
    labels:
      prometheus.io/scrape: "true"
      traefik.http.routers.stale.rule: Host(` + "`old`" + `)
  - id: be/go-core
    members:
      - id: conformance/widget2
  - id: erp/off
    mode: disable
`

func targets(t *testing.T) map[string]Target {
	w := widgetTarget(t)
	w2 := Target{ID: "conformance/widget2", Port: 8081, Routes: []component.EdgeRoute{{Path: "/conformance/widget2/**"}}}
	off := Target{ID: "erp/off", Port: 9, Routes: []component.EdgeRoute{{Path: "/erp/off/**"}}}
	return map[string]Target{"conformance/widget": w, "conformance/widget2": w2, "erp/off": off}
}

func entry(t *testing.T, src []byte, id string) map[string]any {
	t.Helper()
	var doc struct {
		Components []map[string]any `yaml:"components"`
	}
	if err := yaml.Unmarshal(src, &doc); err != nil {
		t.Fatal(err)
	}
	var walk func([]map[string]any) map[string]any
	walk = func(es []map[string]any) map[string]any {
		for _, e := range es {
			if e["id"] == id {
				return e
			}
			if ms, ok := e["members"].([]any); ok {
				var sub []map[string]any
				for _, m := range ms {
					sub = append(sub, m.(map[string]any))
				}
				if r := walk(sub); r != nil {
					return r
				}
			}
		}
		return nil
	}
	return walk(doc.Components)
}

func TestApplyDocker_LabelsOnEntriesAndShellAndCheck(t *testing.T) {
	tg := targets(t)
	if d, err := Check([]byte(dockerDeploy), tg, settings); err != nil || len(d) == 0 {
		t.Fatalf("seen red first: %v %v", d, err)
	}
	out, err := Apply([]byte(dockerDeploy), tg, settings)
	if err != nil {
		t.Fatal(err)
	}
	w := entry(t, out, "conformance/widget")["labels"].(map[string]any)
	if w["prometheus.io/scrape"] != "true" || w["traefik.http.routers.stale.rule"] != nil || w["traefik.http.routers.conformance-widget-1.rule"] == nil {
		t.Errorf("widget labels = %v", w)
	}
	shell := entry(t, out, "be/go-core")["labels"].(map[string]any)
	if shell["traefik.http.services.conformance-widget2.loadbalancer.server.port"] != "8081" {
		t.Errorf("a member's routers belong on the shell entry too: %v", shell)
	}
	member := entry(t, out, "conformance/widget2")["labels"].(map[string]any)
	if member["traefik.http.routers.conformance-widget2-0.rule"] == nil {
		t.Errorf("and on the member's own entry: %v", member)
	}
	if entry(t, out, "erp/off")["labels"] != nil {
		t.Error("a disabled component contributes nothing")
	}
	if !strings.Contains(string(out), "# docker | podman | k8s") {
		t.Error("comments must survive")
	}
	if d, _ := Check(out, tg, settings); len(d) != 0 {
		t.Errorf("after apply: %v", d)
	}
	again, _ := Apply(out, tg, settings)
	if string(again) != string(out) {
		t.Error("not idempotent")
	}
}

const k8sDeploy = `target: k8s
k8s:
  namespace: shop
components:
  - id: conformance/widget
  - id: frontend/standard
  - id: erp/internal
    expose: true
    hostname: old.example.com
    labels:
      traefik.enable: "true"
`

func TestApplyK8s_PathsHostAndTLS(t *testing.T) {
	tg := map[string]Target{
		"conformance/widget": widgetTarget(t),
		"frontend/standard":  {ID: "frontend/standard", Port: 80, Routes: []component.EdgeRoute{{Path: "/**"}}},
		"erp/internal":       {ID: "erp/internal", Port: 1},
	}
	s := settings
	s.IngressAnnotations = map[string]string{"traefik.ingress.kubernetes.io/router.middlewares": "shop-be-edge@kubernetescrd"}
	out, err := Apply([]byte(k8sDeploy), tg, s)
	if err != nil {
		t.Fatal(err)
	}
	w := entry(t, out, "conformance/widget")
	if w["expose"] != true || w["hostname"] != "app.example.com" || w["tlsSecret"] != "app-tls" {
		t.Errorf("widget = %v", w)
	}
	if p, _ := w["paths"].([]any); len(p) != 2 || p[0] != "/conformance/widget/kinds" || p[1] != "/conformance/widget" {
		t.Errorf("paths = %v", w["paths"])
	}
	if f := entry(t, out, "frontend/standard"); f["paths"] != nil || f["expose"] != true {
		t.Errorf("the fallback takes the whole host: %v", f)
	}
	if i := entry(t, out, "erp/internal"); i["expose"] != nil || i["hostname"] != nil || i["labels"] != nil {
		t.Errorf("a component without routes is not exposed: %v", i)
	}
	if !strings.Contains(string(out), "shop-be-edge@kubernetescrd") {
		t.Error("ingressAnnotations not written")
	}
	if d, _ := Check(out, tg, s); len(d) != 0 {
		t.Errorf("after apply: %v", d)
	}
	if _, err := Apply([]byte(k8sDeploy), tg, Settings{}); err == nil {
		t.Error("k8s without a host name must be refused")
	}
}

func TestCoverage_OpenAPIPathsUnderPrefixes(t *testing.T) {
	c, err := component.Load(testfixture.Copy(t, "widget"))
	if err != nil {
		t.Fatal(err)
	}
	if p, err := Coverage(c); err != nil || len(p) != 0 {
		t.Errorf("widget paths are covered: %v %v", p, err)
	}
	c.Assembly.EdgeRoutes = []component.EdgeRoute{{Path: "/conformance/widget/kinds/**"}}
	p, _ := Coverage(c)
	if len(p) == 0 || !strings.Contains(strings.Join(p, "\n"), "/conformance/widget/widgets") {
		t.Errorf("uncovered paths must be named: %v", p)
	}
}

func TestMiddlewareFile_BeEdgeChain(t *testing.T) {
	f := string(MiddlewareFile(Settings{}))
	for _, want := range []string{"be-edge:", "chain:", "be-edge-strip", "X-Request-Id: \"\"", "be-caller: \"\"", "average: 100", "burst: 200"} {
		if !strings.Contains(f, want) {
			t.Errorf("edge.yml lacks %q:\n%s", want, f)
		}
	}
}
