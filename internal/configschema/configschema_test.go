package configschema

import (
	"reflect"
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/protocol"
	"github.com/brickKit/be-ops/internal/testfixture"
)

func setup(t *testing.T, dir string) (*protocol.Catalogue, *component.Component) {
	t.Helper()
	cat, err := protocol.LoadCatalogue()
	if err != nil {
		t.Fatal(err)
	}
	c, err := component.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cat, c
}

func TestProfiles_WidgetUsesEveryProfile(t *testing.T) {
	cat, c := setup(t, testfixture.Copy(t, "widget"))
	b, err := Generate(cat, c)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"core", "obs", "err", "auth", "grpc", "events-pub", "events-sub", "db", "jobs", "lifecycle", "blob"}
	if !reflect.DeepEqual(b.Profiles, want) {
		t.Errorf("profiles = %v, want %v", b.Profiles, want)
	}
	if !reflect.DeepEqual(b.OptIns, []string{"AUTHZ_GRPC_URL"}) {
		t.Errorf("opt-ins = %v (the widget owns a shareable type)", b.OptIns)
	}
}

func TestCheck_PublishedWidgetBlockIsCurrent(t *testing.T) {
	cat, c := setup(t, testfixture.Copy(t, "widget"))
	b, _ := Generate(cat, c)
	if d := Check(cat, c, b); len(d) != 0 {
		t.Errorf("widget should be current:\n%s", strings.Join(d, "\n"))
	}
}

func TestCheck_ReportsMissingRetiredAndWrongDefault(t *testing.T) {
	dir := testfixture.Copy(t, "widget")
	src := testfixture.Read(t, dir, "component.yaml")
	src = strings.Replace(src, "    PG_OWNER_USER: {type: string}\n", "", 1)
	src = strings.Replace(src, "PG_PORT: {type: integer, default: 5432}", "PG_PORT: {type: string, default: \"5433\"}", 1)
	src = strings.Replace(src, "    IAM_URL: {type: string}\n", "    IAM_URL: {type: string}\n    AUTHZ_BUNDLE_URL: {type: string}\n", 1)
	testfixture.Write(t, dir, "component.yaml", src)
	cat, c := setup(t, dir)
	b, _ := Generate(cat, c)
	d := strings.Join(Check(cat, c, b), "\n")
	for _, want := range []string{"PG_OWNER_USER", "PG_PORT", "AUTHZ_BUNDLE_URL"} {
		if !strings.Contains(d, want) {
			t.Errorf("check should name %s:\n%s", want, d)
		}
	}
}

func TestCheck_OwnKeysFollowSecretAndReservedRules(t *testing.T) {
	dir := testfixture.Copy(t, "widget")
	src := testfixture.Read(t, dir, "component.yaml")
	src = strings.Replace(src, "    WIDGET_APPROVE_TIMEOUT:", "    WIDGET_TOKEN: {type: string, secret: true}\n    WIDGET_CERT_FILE: {type: string}\n    PEER_ENDPOINT: {type: string}\n    WIDGET_APPROVE_TIMEOUT:", 1)
	testfixture.Write(t, dir, "component.yaml", src)
	cat, c := setup(t, dir)
	b, _ := Generate(cat, c)
	d := strings.Join(Check(cat, c, b), "\n")
	for _, want := range []string{"WIDGET_TOKEN", "WIDGET_CERT_FILE", "PEER_ENDPOINT"} {
		if !strings.Contains(d, want) {
			t.Errorf("check should refuse %s:\n%s", want, d)
		}
	}
}

const legacy = `apiVersion: brickkit/v1
kind: Component
metadata:
  id: erp/sales
  version: 3.0.0
# config comment kept
configSchema:
  type: object
  properties:
    PG_HOST: {type: string}
    PG_PASSWORD: {type: string, secret: true}
    AUTHZ_BUNDLE_URL: {type: string}
    PG_SCHEMA: {type: string}
    # the component's own keys
    DEFAULT_WAREHOUSE_ID: {type: string, default: "1"}   # kept
    SALES_NO_FORMAT: {type: string}
  required: [PG_HOST, PG_PASSWORD, DEFAULT_WAREHOUSE_ID]
deployment:
  port: 8084
  extraPorts:
    - {name: grpc, port: 9084, protocol: grpc}
`

func TestApply_RewritesLegacyBlockKeepingOwnKeys(t *testing.T) {
	dir := t.TempDir()
	testfixture.Write(t, dir, "component.yaml", legacy)
	testfixture.Write(t, dir, "assembly.yaml", "edge_routes:\n  - { path: /erp/sales/**, auth: required }\n")
	cat, c := setup(t, dir)
	b, err := Generate(cat, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(Check(cat, c, b)) == 0 {
		t.Fatal("seen red first: the legacy block must be stale")
	}
	out, err := Apply(c, b)
	if err != nil {
		t.Fatal(err)
	}
	testfixture.Write(t, dir, "component.yaml", string(out))
	cat, c2 := setup(t, dir)
	if d := Check(cat, c2, b); len(d) != 0 {
		t.Fatalf("after apply:\n%s\n%s", strings.Join(d, "\n"), out)
	}
	s := string(out)
	for _, want := range []string{"# config comment kept", "DEFAULT_WAREHOUSE_ID: {type: string, default: \"1\"}", "# kept",
		"PG_OWNER_PASSWORD_FILE: {type: string, secret: true, mount: file}", "GRPC_MAX_CONNECTION_AGE", "deployment:\n  port: 8084"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	for _, gone := range []string{"AUTHZ_BUNDLE_URL", "PG_PASSWORD:", "S3_URL"} {
		if strings.Contains(s, gone) {
			t.Errorf("output still has %q", gone)
		}
	}
	if !c2.IsRequired("DEFAULT_WAREHOUSE_ID") || c2.IsRequired("PG_PASSWORD") || !c2.IsRequired("PG_OWNER_USER") {
		t.Errorf("required list wrong: %v", c2.Required)
	}
	b2, _ := Generate(cat, c2)
	again, _ := Apply(c2, b2)
	if string(again) != s {
		t.Errorf("not idempotent:\n%s\n---\n%s", s, again)
	}
}
