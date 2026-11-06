package recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brickKit/be-sdk-go/v3/runprofile"
)

// RecordBackup is the backup tool's record of a successful backup in the run profile (kind backup, be-protocol
// P15.21): the components release their archive units' online rows only after a backup that finished after the units
// were published. Not a recovery step; pg-backup.sh calls it after each successful backup.
func (t *Tool) RecordBackup(ctx context.Context, backup string, finished time.Time) error {
	if backup == "" || finished.IsZero() {
		return errors.New("backup-record needs --backup and --finished-at")
	}
	at := finished.UTC().Format(time.RFC3339Nano)
	if _, err := t.Profile.Append(ctx, runprofile.KindBackup, map[string]string{"finished_at": at, "backup": backup}); err != nil {
		return err
	}
	fmt.Fprintf(t.Out, "backup %s recorded (finished %s)\n", backup, at)
	return nil
}
