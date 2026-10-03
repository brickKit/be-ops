package dbscript

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// These tests run the generated script against a real, throwaway PostgreSQL 16 container named
// by BEOPS_PG16_CONTAINER (for example: docker run -d --name sdkb-ops-pg16
// -e POSTGRES_PASSWORD=x postgres:16-alpine). Never point it at a shared database.

const testPassword = "beops-test-pw"

func container(t *testing.T) string {
	c := os.Getenv("BEOPS_PG16_CONTAINER")
	if c == "" {
		t.Skip("BEOPS_PG16_CONTAINER not set: real-PostgreSQL tests skipped")
	}
	return c
}

// psql runs SQL in the container as user (superuser postgres when password is empty).
func psql(t *testing.T, c, user, db, sql string) (string, error) {
	t.Helper()
	args := []string{"exec", "-i"}
	host := []string{}
	if user != "postgres" { // the superuser uses the local socket; every other role logs in with its password over TCP
		args = append(args, "-e", "PGPASSWORD="+testPassword)
		host = []string{"-h", "127.0.0.1"}
	}
	args = append(args, c, "psql", "-X", "-q", "-t", "-A", "-v", "ON_ERROR_STOP=1")
	args = append(append(args, host...), "-U", user, "-d", db, "-f", "-")
	cmd := exec.Command("docker", args...)
	cmd.Stdin = strings.NewReader(sql)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func runScript(t *testing.T, c string, plan Plan) {
	t.Helper()
	s, err := Render(plan)
	if err != nil {
		t.Fatal(err)
	}
	var sets strings.Builder
	for _, v := range PasswordVars(plan) {
		name, _, _ := strings.Cut(v, "\t")
		fmt.Fprintf(&sets, "\\set %s '%s'\n", name, testPassword)
	}
	if out, err := psql(t, c, "postgres", "postgres", sets.String()+s); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
}

func mustOK(t *testing.T, c, user, sql string) string {
	t.Helper()
	out, err := psql(t, c, user, "beops_it", sql)
	if err != nil {
		t.Fatalf("%s: %q failed: %v\n%s", user, sql, err, out)
	}
	return out
}

func mustFail(t *testing.T, c, user, sql, want string) {
	t.Helper()
	out, err := psql(t, c, user, "beops_it", sql)
	if err == nil || !strings.Contains(out, want) {
		t.Errorf("%s: %q should fail with %q, got err=%v\n%s", user, sql, want, err, out)
	}
}

func TestRealPostgres16_OwnerRuntimeShell(t *testing.T) {
	c := container(t)
	plan, err := Build(project(t, nil), "beops_it")
	if err != nil {
		t.Fatal(err)
	}
	runScript(t, c, plan)
	runScript(t, c, plan) // idempotent

	mustOK(t, c, "sales_own", "CREATE TABLE IF NOT EXISTS sales.orders (id bigserial PRIMARY KEY, note text);")
	mustOK(t, c, "sales_rt", "INSERT INTO sales.orders (note) VALUES ('ok'); SELECT count(*) FROM sales.orders;")
	mustFail(t, c, "sales_rt", "DROP TABLE sales.orders;", "must be owner")
	mustFail(t, c, "sales_rt", "CREATE TABLE sales.evil (id int);", "permission denied")
	mustFail(t, c, "sales_rt", "ALTER TABLE sales.orders ADD COLUMN x int;", "must be owner")
	if got := mustOK(t, c, "sales_rt", "SELECT pg_has_role('sales_rt', 'sales_own', 'MEMBER');"); got != "f" {
		t.Errorf("the runtime role must not be a member of the owner, got %s", got)
	}
	mustFail(t, c, "cust_rt", "SELECT * FROM sales.orders;", "permission denied")

	mustFail(t, c, "core_login", "SELECT * FROM sales.orders;", "permission denied")
	mustOK(t, c, "core_login", "BEGIN; SET LOCAL ROLE sales_rt; SET LOCAL search_path TO sales; SELECT count(*) FROM orders; COMMIT;")
	mustFail(t, c, "core_login", "SET ROLE sales_own;", "permission denied")

	if got := mustOK(t, c, "postgres", "SELECT array_to_string(rolconfig, ',') FROM pg_roles WHERE rolname = 'core_login';"); !strings.Contains(got, "statement_timeout=30s") ||
		!strings.Contains(got, "idle_in_transaction_session_timeout=60s") || !strings.Contains(got, "lock_timeout=5s") {
		t.Errorf("role-level timeouts = %s", got)
	}
	if got := mustOK(t, c, "postgres", "SELECT count(*) FROM pg_namespace WHERE nspname LIKE '%_archive';"); got != "0" {
		t.Errorf("no _archive schema, got %s", got)
	}
}

func TestRealPostgres16_RepairsAnOwnerMembership(t *testing.T) {
	c := container(t)
	plan, err := Build(project(t, nil), "beops_it")
	if err != nil {
		t.Fatal(err)
	}
	runScript(t, c, plan)
	mustOK(t, c, "postgres", "GRANT sales_own TO sales_rt; GRANT sales_rt TO core_login WITH INHERIT TRUE;")
	runScript(t, c, plan)
	if got := mustOK(t, c, "postgres", "SELECT bool_or(m.inherit_option) FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid "+
		"JOIN pg_roles u ON u.oid = m.member WHERE r.rolname = 'sales_rt' AND u.rolname = 'core_login';"); got != "f" {
		t.Errorf("re-running the script must turn a 2.x inheriting shell grant into INHERIT FALSE, got %s", got)
	}
	if got := mustOK(t, c, "postgres", "SELECT pg_has_role('sales_rt', 'sales_own', 'MEMBER');"); got != "f" {
		t.Errorf("re-running the script must revoke the membership, got %s", got)
	}
}

func TestRealPostgres16_BusSchemaPublishAndConsume(t *testing.T) {
	c := container(t)
	plan, err := Build(project(t, busProject(t, nil)), "beops_it")
	if err != nil {
		t.Fatal(err)
	}
	runScript(t, c, plan)
	runScript(t, c, plan) // idempotent

	publish := "BEGIN; INSERT INTO be_bus.msg_id (stream, msg_id, expires_at) VALUES ('BE_ERP', 'm1-' || txid_current(), now() + interval '10 minutes') ON CONFLICT DO NOTHING RETURNING 1;" +
		" INSERT INTO be_bus.message (stream, msg_id, subject, headers, data) VALUES ('BE_ERP', 'm1', 'erp.sales.order.created.v1', '{}', '\\x7b7d'); COMMIT;"
	mustOK(t, c, "sales_rt", publish)
	mustOK(t, c, "core_login", publish)
	consume := "BEGIN; INSERT INTO be_bus.durable (name, stream, filter) VALUES ('d1', 'BE_ERP', 'erp.>') ON CONFLICT DO NOTHING;" +
		" SELECT 1 FROM be_bus.durable WHERE name = 'd1' FOR UPDATE;" +
		" INSERT INTO be_bus.delivery (durable, seq) SELECT 'd1', seq FROM be_bus.message WHERE tx_id < pg_snapshot_xmin(pg_current_snapshot()) ON CONFLICT DO NOTHING;" +
		" UPDATE be_bus.durable SET last_seq = (SELECT max(seq) FROM be_bus.message) WHERE name = 'd1';" +
		" SELECT seq FROM be_bus.delivery WHERE durable = 'd1' AND next_at <= now() FOR UPDATE SKIP LOCKED;" +
		" DELETE FROM be_bus.delivery WHERE durable = 'd1'; DELETE FROM be_bus.msg_id WHERE expires_at < now(); COMMIT;"
	mustOK(t, c, "sales_rt", consume)

	mustFail(t, c, "cust_rt", "SELECT count(*) FROM be_bus.message;", "permission denied")
	mustFail(t, c, "sales_own", "SELECT count(*) FROM be_bus.message;", "permission denied")
	mustFail(t, c, "sales_rt", "DROP TABLE be_bus.delivery;", "must be owner")
	mustFail(t, c, "sales_rt", "CREATE TABLE be_bus.evil (id int);", "permission denied")
	if got := mustOK(t, c, "postgres", "SELECT string_agg(DISTINCT pg_get_userbyid(c.relowner), ',') FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace"+
		" WHERE n.nspname = 'be_bus' AND c.relkind IN ('r', 'p', 'S');"); got != "be_bus_owner" {
		t.Errorf("be_bus objects owned by %q, want be_bus_owner", got)
	}
	if got := mustOK(t, c, "postgres", "SELECT rolcanlogin FROM pg_roles WHERE rolname = 'be_bus_owner';"); got != "f" {
		t.Errorf("be_bus_owner must not log in, got %s", got)
	}
}
