package configschema

import (
	"reflect"
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/testfixture"
)

const shellManifest = `apiVersion: brickkit/v1
kind: Component
metadata:
  id: be/go-test
  version: 1.1.0
shell:
  members: [%s]
configSchema:
  type: object
  properties:
    PG_PASSWORD: {type: string, secret: true}
    SHELL_OWN_KEY: {type: string, default: x}
  required: [PG_PASSWORD]
deployment:
  port: 8090
  stopGracePeriodSeconds: %s
`

func shellSetup(t *testing.T, members, grace string) (*component.Component, []*component.Component) {
	t.Helper()
	dir := t.TempDir()
	testfixture.Write(t, dir, "component.yaml", strings.Replace(strings.Replace(shellManifest, "%s", members, 1), "%s", grace, 1))
	sh, err := component.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ms []*component.Component
	if members != "" {
		w, err := component.Load(testfixture.Copy(t, "widget"))
		if err != nil {
			t.Fatal(err)
		}
		ms = append(ms, w)
	}
	return sh, ms
}

func names(b Block) map[string]Prop {
	out := map[string]Prop{}
	for _, p := range b.Props {
		out[p.Name] = p
	}
	return out
}

func TestGenerateShell_KeysAreTheProcessWideOnesOfTheMembersUnion(t *testing.T) {
	cat, _ := setup(t, testfixture.Copy(t, "widget"))
	sh, ms := shellSetup(t, "conformance/widget@1.0.0", "30")
	b, err := GenerateShell(cat, sh, ms)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"core", "obs", "err", "auth", "grpc", "events-pub", "events-sub", "db", "jobs", "lifecycle", "blob"}
	if !reflect.DeepEqual(b.Profiles, want) {
		t.Errorf("profiles = %v, want the members' union %v", b.Profiles, want)
	}
	got := names(b)
	for _, k := range []string{"PG_HOST", "PG_PORT", "PG_DATABASE", "PG_USER", "PG_PASSWORD_FILE", "PG_POOL_MAX", "PG_CONN_MAX_LIFETIME",
		"EVENT_BUS_URL", "NATS_URL", "AUTHZ_URL", "IAM_URL", "IAM_ISSUER", "TENANT_ID",
		"OTEL_BASE_URL", "LOG_LEVEL", "DEFAULT_LOCALE", "SHUTDOWN_GRACE", "HTTP_DEFAULT_TIMEOUT"} {
		if _, ok := got[k]; !ok {
			t.Errorf("shell lacks %s", k)
		}
	}
	for _, k := range []string{"PG_SCHEMA", "PG_OWNER_USER", "PG_OWNER_PASSWORD_FILE", "PG_MIGRATION_HOST", "PG_POOL_ACQUIRE_TIMEOUT",
		"EVENTS_MAX_DELIVER", "EVENTS_BACKOFF", "AUTHZ_GRPC_URL", "S3_URL", "S3_BUCKET", "GRPC_MAX_CONNECTION_AGE",
		"JOBS_OVERRIDES", "BUSINESS_TIMEZONE", "DATA_LIFECYCLE"} {
		if _, ok := got[k]; ok {
			t.Errorf("shell must not declare the per-member key %s", k)
		}
	}
	if d := got["PG_POOL_MAX"].Default; d == nil || *d != "40" {
		t.Errorf("a shell's PG_POOL_MAX is the physical pool, default 40; got %v", d)
	}
	req := strings.Join(b.Required, ",")
	if !strings.Contains(req, "PG_USER") || !strings.Contains(req, "AUTHZ_URL") || strings.Contains(req, "PG_SCHEMA") {
		t.Errorf("required = %v", b.Required)
	}
}

func TestGenerateShell_ZeroMembersNeedOnlyTheProcessKeys(t *testing.T) {
	cat, _ := setup(t, testfixture.Copy(t, "widget"))
	sh, _ := shellSetup(t, "", "30")
	b, err := GenerateShell(cat, sh, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.Profiles, []string{"core", "obs", "err"}) {
		t.Errorf("profiles = %v", b.Profiles)
	}
	if _, ok := names(b)["PG_HOST"]; ok {
		t.Error("no member with a database: no PG keys")
	}
}

func TestCheckShell_ApplyRepairsAndGraceCoversTheSlowestMember(t *testing.T) {
	cat, _ := setup(t, testfixture.Copy(t, "widget"))
	sh, ms := shellSetup(t, "conformance/widget@1.0.0", "30")
	b, _ := GenerateShell(cat, sh, ms)
	if d := CheckShell(cat, sh, ms, b); len(d) == 0 {
		t.Fatal("seen red first: the hand-written block is stale")
	}
	out, err := Apply(sh, b)
	if err != nil {
		t.Fatal(err)
	}
	testfixture.Write(t, sh.Dir, "component.yaml", string(out))
	sh2, _ := component.Load(sh.Dir)
	if d := CheckShell(cat, sh2, ms, b); len(d) != 0 {
		t.Fatalf("after apply:\n%s\n%s", strings.Join(d, "\n"), out)
	}
	if !strings.Contains(string(out), "SHELL_OWN_KEY: {type: string, default: x}") || strings.Contains(string(out), "PG_PASSWORD:") {
		t.Errorf("own key kept, retired key dropped:\n%s", out)
	}
	if !strings.Contains(string(out), "members' union") {
		t.Errorf("the marker says the profiles are the members' union:\n%s", out)
	}
	short := strings.Replace(string(out), "stopGracePeriodSeconds: 30", "stopGracePeriodSeconds: 20", 1)
	testfixture.Write(t, sh.Dir, "component.yaml", short)
	sh3, _ := component.Load(sh.Dir)
	if d := strings.Join(CheckShell(cat, sh3, ms, b), "\n"); !strings.Contains(d, "stopGracePeriodSeconds") || !strings.Contains(d, "conformance/widget") {
		t.Errorf("a shell's grace below its slowest member's must be named (P19.9): %s", d)
	}
}
