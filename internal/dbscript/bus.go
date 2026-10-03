package dbscript

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/brickKit/be-ops/internal/projconf"
	beprotocol "github.com/brickKit/be-protocol"
)

// BusSchema is the schema of the PostgreSQL queue; the reference DDL names it (be-protocol
// ddl/be_bus.sql, P12.12).
const BusSchema = "be_bus"

// BusOwner is the NOLOGIN role that owns be_bus and its objects: the schema belongs to the
// infrastructure, like NATS, so no component and no shell owns it, and nobody logs in as it.
const BusOwner = "be_bus_owner"

// Bus is the PostgreSQL queue the project uses, when it uses one.
type Bus struct {
	URL      string   // the EVENT_BUS_URL every user shares
	Database string   // where the be_bus schema lives
	Roles    []string // the login roles that publish and consume: runtime roles and shell logins
}

// busOf decides from EVENT_BUS_URL whether the project uses the PostgreSQL queue. Every component
// that declares the key must use the same adapter and the same URL (P12.12); the roles granted are
// the runtime roles of those components and the login roles of the shells whose own EVENT_BUS_URL
// is the queue (a shell's bus connection is process-wide, P19.3).
func busOf(p *projconf.Project, ids []Identity, shells []ShellLogin, database string) (*Bus, error) {
	type user struct{ id, role string }
	var users []user
	for _, i := range ids {
		users = append(users, user{i.ID, i.Runtime})
	}
	for _, s := range shells {
		users = append(users, user{s.ID, s.Login})
	}
	var bus *Bus
	var first, firstURL string
	for _, u := range users {
		v, err := p.Resolve(u.id, "EVENT_BUS_URL")
		if err != nil {
			return nil, err
		}
		if v.Kind == projconf.Absent {
			continue
		}
		if v.Kind != projconf.Literal {
			return nil, fmt.Errorf("%s: EVENT_BUS_URL must be a literal URL (or a $var: reference to one) in %s", u.id, p.ConfigPath(u.id))
		}
		if first == "" {
			first, firstURL = u.id, v.Value
		} else if v.Value != firstURL {
			return nil, fmt.Errorf("%s and %s have different EVENT_BUS_URL values: every component of a project uses the same bus (P12.12)", first, u.id)
		}
		if !strings.HasPrefix(v.Value, "postgres://") && !strings.HasPrefix(v.Value, "postgresql://") {
			continue
		}
		if bus == nil {
			if bus, err = parseBus(u.id, v.Value, database); err != nil {
				return nil, err
			}
		}
		bus.Roles = append(bus.Roles, u.role)
	}
	if bus != nil {
		sort.Strings(bus.Roles)
	}
	return bus, nil
}

func parseBus(id, raw, database string) (*Bus, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: EVENT_BUS_URL: %w", id, err)
	}
	if s := u.Query().Get("schema"); s != BusSchema {
		return nil, fmt.Errorf("%s: EVENT_BUS_URL schema=%q: the PostgreSQL queue lives in schema %s (be-protocol ddl/be_bus.sql)", id, s, BusSchema)
	}
	b := &Bus{URL: raw, Database: strings.TrimPrefix(u.Path, "/")}
	if database != "" {
		b.Database = database
	}
	if b.Database == "" {
		return nil, fmt.Errorf("%s: EVENT_BUS_URL %q names no database", id, raw)
	}
	return b, nil
}

// renderBusOwner creates the owner role (cluster-wide section).
func renderBusOwner(b *strings.Builder) {
	fmt.Fprintf(b, "-- event bus: %s owns schema %s and logs in nowhere\n", BusOwner, BusSchema)
	fmt.Fprintf(b, "DO $be$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %s) THEN CREATE ROLE %s NOLOGIN; END IF; END $be$;\n\n",
		lit(BusOwner), ident(BusOwner))
}

// renderBus runs the protocol's DDL as it is published, adds a DEFAULT partition so a publish
// never fails for want of a partition, hands every object to the owner role and grants each user
// what the DDL states: SELECT, INSERT, UPDATE, DELETE on the tables and USAGE on the sequence.
func renderBus(b *strings.Builder, bus *Bus) error {
	ddl, err := beprotocol.FS.ReadFile("ddl/be_bus.sql")
	if err != nil {
		return err
	}
	b.WriteString("-- event bus: the PostgreSQL queue, be-protocol ddl/be_bus.sql as published\n")
	b.Write(ddl)
	if !strings.HasSuffix(string(ddl), "\n") {
		b.WriteString("\n")
	}
	o := ident(BusOwner)
	fmt.Fprintf(b, "CREATE TABLE IF NOT EXISTS %s.message_default PARTITION OF %s.message DEFAULT;\n", BusSchema, BusSchema)
	fmt.Fprintf(b, "ALTER SCHEMA %s OWNER TO %s;\n", BusSchema, o)
	fmt.Fprintf(b, "DO $be$ DECLARE r record; BEGIN FOR r IN SELECT c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace"+
		" WHERE n.nspname = %s AND c.relkind IN ('r', 'p', 'S') LOOP"+
		" EXECUTE format('ALTER %%s %%I.%%I OWNER TO %%I', CASE r.relkind WHEN 'S' THEN 'SEQUENCE' ELSE 'TABLE' END, %s, r.relname, %s);"+
		" END LOOP; END $be$;\n", lit(BusSchema), lit(BusSchema), lit(BusOwner))
	fmt.Fprintf(b, "REVOKE ALL ON SCHEMA %s FROM PUBLIC;\n", BusSchema)
	for _, r := range bus.Roles {
		fmt.Fprintf(b, "GRANT USAGE ON SCHEMA %s TO %s;\n", BusSchema, ident(r))
		fmt.Fprintf(b, "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s;\n", BusSchema, ident(r))
		fmt.Fprintf(b, "GRANT USAGE ON SEQUENCE %s.message_seq TO %s;\n", BusSchema, ident(r))
	}
	b.WriteString("\n")
	return nil
}
