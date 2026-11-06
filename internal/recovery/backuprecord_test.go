package recovery

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/brickKit/be-sdk-go/v3/tiering"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P15.21: what backup-record writes is what the SDK's release reads (tiering.BackupsFrom): the latest finished backup.
func TestABackupRecordIsWhatTheReleaseReads(t *testing.T) {
	ctx := context.Background()
	prof := &fakeProfile{}
	tool := &Tool{Profile: prof, Out: &bytes.Buffer{}}
	_, ok, err := tiering.BackupsFrom(prof)(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "no backup recorded yet")

	first := time.Date(2026, 11, 6, 2, 0, 0, 0, time.FixedZone("CET", 3600))
	second := first.Add(24 * time.Hour)
	require.NoError(t, tool.RecordBackup(ctx, "base-2", second))
	require.NoError(t, tool.RecordBackup(ctx, "base-1", first))
	latest, ok, err := tiering.BackupsFrom(prof)(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, latest.Equal(second), "the latest backup, not the last written: %s", latest)

	assert.Error(t, tool.RecordBackup(ctx, "", second), "a backup without its label")
	assert.Error(t, tool.RecordBackup(ctx, "base-3", time.Time{}), "a backup without its time")
	assert.Len(t, prof.recs["backup"], 2)
}
