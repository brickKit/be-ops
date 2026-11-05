//go:build integration

package recovery

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/brickKit/be-sdk-go/v3/runprofile"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const itDatabase = "beops_recover_it"

// itConn connects to the test database (BE_SDK_TEST_PG_ADMIN, the SDK's integration tests' administrator).
func itConn(t *testing.T) func(ctx context.Context, database string) (*pgx.Conn, error) {
	admin := os.Getenv("BE_SDK_TEST_PG_ADMIN")
	require.NotEmpty(t, admin, "BE_SDK_TEST_PG_ADMIN is not set")
	u, err := url.Parse(admin)
	require.NoError(t, err)
	ctx := context.Background()
	c, err := pgx.Connect(ctx, admin)
	require.NoError(t, err)
	_, _ = c.Exec(ctx, "DROP DATABASE IF EXISTS "+itDatabase+" WITH (FORCE)")
	_, err = c.Exec(ctx, "CREATE DATABASE "+itDatabase)
	require.NoError(t, err)
	c.Close(ctx)
	return func(ctx context.Context, database string) (*pgx.Conn, error) {
		v := *u
		v.Path = "/" + database
		return pgx.Connect(ctx, v.String())
	}
}

// Steps 4 and 5 against real databases (the components' tables as the SDK's migrations make them, reduced to what the
// tool reads): advance writes the run profile first and then every database that went back, the evidence store's
// excepted, and the authorization's generation; redeliver deletes the streams, restarts redelivering and waits until
// every consumer has recorded its database's epoch, listing the gaps.
func TestAdvanceAndRedeliver(t *testing.T) {
	ctx := context.Background()
	conn := itConn(t)
	p := project(t)
	for _, c := range p.Components {
		p.Components[indexOf(p, c.ID)].Database = itDatabase
	}
	c, err := conn(ctx, itDatabase)
	require.NoError(t, err)
	for _, s := range []string{"infra_audit", "mdm_org", "infra_authz"} {
		_, err = c.Exec(ctx, `CREATE SCHEMA `+s+`;
			CREATE TABLE `+s+`.be_recovery_generation (generation bigint NOT NULL); INSERT INTO `+s+`.be_recovery_generation VALUES (1);
			CREATE TABLE `+s+`.be_outbox_partition (stream text, partition int);
			CREATE TABLE `+s+`.be_subscription_run (subscription text);
			CREATE TABLE `+s+`.be_consumer_epoch (epoch_generation bigint, epoch_timeline bigint);
			CREATE TABLE `+s+`.be_resend_gap (id bigint GENERATED ALWAYS AS IDENTITY, subscription text, source_component text,
				source_stream text, source_partition int, kind text)`)
		require.NoError(t, err)
	}
	_, err = c.Exec(ctx, `CREATE TABLE infra_authz.authz_state (recovery_generation bigint); INSERT INTO infra_authz.authz_state VALUES (3);
		INSERT INTO mdm_org.be_outbox_partition VALUES ('mdm.org', 0), ('mdm.org', 1);
		INSERT INTO infra_authz.be_subscription_run VALUES ('accounts'); INSERT INTO infra_authz.be_consumer_epoch VALUES (1, 1);
		INSERT INTO mdm_org.be_subscription_run VALUES ('x'); INSERT INTO mdm_org.be_consumer_epoch VALUES (1, 1)`)
	require.NoError(t, err)

	prof := &fakeProfile{}
	var deleted []string
	phase := ""
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	tool := &Tool{Project: p, Profile: prof, Conn: conn, Out: &bytes.Buffer{}, Wait: 6 * time.Second,
		Now:  func() time.Time { now = now.Add(time.Second); return now },
		Exec: func(context.Context, string, string, ...string) (string, error) { phase = "redelivering"; return "", nil },
		Phases: func(context.Context) (map[string]string, error) {
			return map[string]string{"mdm/org": phase}, nil
		},
		DeleteStream: func(_ context.Context, s string) error { deleted = append(deleted, s); return nil }}
	// the steps before, as the run profile records them
	for _, s := range []Step{{Step: StepAnnounce, Recovery: "r", T: "2026-11-05T10:00:00Z"}, {Step: StepFreeze}, {Step: StepRestoreDB}} {
		s.State, s.At = After(s.Step), tool.now().Format(AtLayout)
		_, err := prof.Append(ctx, runprofile.KindRecoveryStep, s)
		require.NoError(t, err)
	}

	require.NoError(t, tool.Advance(ctx))
	gen := func(schema string) int64 {
		var g int64
		require.NoError(t, c.QueryRow(ctx, "SELECT generation FROM "+schema+".be_recovery_generation").Scan(&g))
		return g
	}
	assert.EqualValues(t, 2, gen("mdm_org"))
	assert.EqualValues(t, 2, gen("infra_authz"))
	assert.EqualValues(t, 1, gen("infra_audit"), "the evidence store's database did not go back")
	var authz int64
	require.NoError(t, c.QueryRow(ctx, "SELECT recovery_generation FROM infra_authz.authz_state").Scan(&authz))
	assert.EqualValues(t, 4, authz)
	recs, _, _ := prof.List(ctx, runprofile.KindDeploymentGeneration)
	require.Len(t, recs, 1, "the run profile first")

	// the authorization member settles, the organization member does not: the step waits, then fails
	_, err = c.Exec(ctx, `UPDATE infra_authz.be_consumer_epoch SET epoch_generation = 2;
		INSERT INTO infra_authz.be_resend_gap (subscription, source_component, source_stream, source_partition, kind)
			VALUES ('accounts', 'infra/iam-casdoor', 'infra.iam.account_status', 2, 'beyond_retention')`)
	require.NoError(t, err)
	tool.Wait = 3 * time.Second
	err = tool.Redeliver(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mdm/org")
	assert.ElementsMatch(t, []string{"mdm.org"}, deleted)
	_, err = c.Exec(ctx, `UPDATE mdm_org.be_consumer_epoch SET epoch_generation = 2`)
	require.NoError(t, err)
	require.NoError(t, tool.Redeliver(ctx))
	recs, _, _ = prof.List(ctx, runprofile.KindRecoveryStep)
	last, _ := Decode(recs[len(recs)-1].Body)
	assert.Equal(t, StepRedeliver, last.Step)
	assert.Equal(t, map[string]any{"infra/authz": []any{"accounts infra/iam-casdoor infra.iam.account_status/2 beyond_retention"}},
		last.Detail["gaps"])
	c.Close(ctx)
}

func indexOf(p *Project, id string) int {
	for i, c := range p.Components {
		if c.ID == id {
			return i
		}
	}
	return -1
}
