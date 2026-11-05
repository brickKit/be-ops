package recovery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func at(s int) string { return time.Date(2026, 11, 5, 10, 0, s, 0, time.UTC).Format(AtLayout) }

// The state table of protocol 15 for steps 1-5: announce from running, abandon only while frozen, the steps in order,
// a step again is allowed, and the records fold in the order they were written.
func TestTheRecoveryStateFolds(t *testing.T) {
	st, err := Fold(nil)
	require.NoError(t, err)
	assert.Equal(t, Running, st.State)

	steps := []Step{
		{Step: StepFreeze, State: Restoring, At: at(2)}, // written out of order: folded by time
		{Step: StepAnnounce, State: Frozen, Recovery: "r1", T: "2026-11-05T09:00:00Z", At: at(1)},
		{Step: StepRestoreDB, State: Restoring, At: at(3)},
		{Step: StepRestoreDB, State: Restoring, At: at(4)},
		{Step: StepAdvance, State: Restoring, At: at(5)},
	}
	st, err = Fold(steps)
	require.NoError(t, err)
	assert.Equal(t, Restoring, st.State)
	assert.Equal(t, "r1", st.Recovery)
	assert.Equal(t, time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC), st.T)
	assert.Equal(t, []string{StepFreeze, StepRestoreDB, StepAdvance}, st.Done, "a repeated step once")
	assert.NoError(t, Allowed(st, StepRedeliver))
	assert.NoError(t, Allowed(st, StepRestoreDB), "each step may be done again")
	assert.Error(t, Allowed(st, StepAbandon), "the databases went back: no giving up")
	assert.Error(t, Allowed(st, StepAnnounce), "one recovery at a time")

	for _, c := range []struct {
		name  string
		state Status
		step  string
	}{
		{"freeze before announcing", Status{State: Running}, StepFreeze},
		{"restore before freezing", Status{State: Frozen}, StepRestoreDB},
		{"advance before restoring", Status{State: Restoring, Done: []string{StepFreeze}}, StepAdvance},
		{"redeliver before advancing", Status{State: Restoring, Done: []string{StepFreeze, StepRestoreDB}}, StepRedeliver},
		{"abandon while running", Status{State: Running}, StepAbandon},
		{"an unknown step", Status{State: Frozen}, "dance"},
	} {
		assert.Error(t, Allowed(c.state, c.step), c.name)
	}
	st, err = Fold([]Step{{Step: StepAnnounce, State: Frozen, Recovery: "r2", At: at(1)}, {Step: StepAbandon, State: Running, At: at(2)}})
	require.NoError(t, err)
	assert.Equal(t, Status{State: Running}, st, "given up: running again, no recovery")
	_, err = Fold([]Step{{Step: StepRedeliver, State: Restoring, At: at(1)}})
	assert.Error(t, err, "records that do not follow the table do not fold")
}

// Steps 7-10 (protocol 15's state table): verifying after step 6, the checks and the loss in it (each again), read-only
// only after both, running again from read-only, the recovery over.
func TestTheStepsAfterTheReplay(t *testing.T) {
	six := []Step{
		{Step: StepAnnounce, State: Frozen, Recovery: "r1", T: "2026-11-05T09:00:00Z", At: at(1)},
		{Step: StepFreeze, State: Restoring, At: at(2)}, {Step: StepRestoreDB, State: Restoring, At: at(3)},
		{Step: StepAdvance, State: Restoring, At: at(4)}, {Step: StepRedeliver, State: Restoring, At: at(5)},
		{Step: StepReplay, State: Restoring, At: at(6)},
	}
	st, err := Fold(six)
	require.NoError(t, err)
	assert.Error(t, Allowed(st, StepReconcile), "keys (6.8) first")
	steps := append(six, Step{Step: StepKeys, State: Restoring, At: at(7)})
	st, err = Fold(steps)
	require.NoError(t, err)
	require.NoError(t, Allowed(st, StepReconcile))
	assert.Error(t, Allowed(st, StepCheck), "reconcile first")
	assert.Error(t, Allowed(st, StepOpenRead))

	steps = append(steps, Step{Step: StepReconcile, State: After(StepReconcile), At: at(8)})
	st, err = Fold(steps)
	require.NoError(t, err)
	assert.Equal(t, Verifying, st.State)
	assert.NoError(t, Allowed(st, StepReconcile), "again")
	assert.NoError(t, Allowed(st, StepCheck))
	assert.NoError(t, Allowed(st, StepLoss))
	assert.Error(t, Allowed(st, StepOpenRead), "the checks and the loss first")
	assert.Error(t, Allowed(st, StepOpen), "read-only first")
	assert.Error(t, Allowed(st, StepReplay), "step 6 is over")

	steps = append(steps, Step{Step: StepCheck, State: After(StepCheck), At: at(9)})
	st, _ = Fold(steps)
	assert.Error(t, Allowed(st, StepOpenRead), "the loss too")
	steps = append(steps, Step{Step: StepLoss, State: After(StepLoss), At: at(10)})
	st, _ = Fold(steps)
	require.NoError(t, Allowed(st, StepOpenRead))

	steps = append(steps, Step{Step: StepOpenRead, State: After(StepOpenRead), At: at(11)})
	st, err = Fold(steps)
	require.NoError(t, err)
	assert.Equal(t, ReadOnly, st.State)
	assert.Error(t, Allowed(st, StepCheck), "verifying is over")
	require.NoError(t, Allowed(st, StepOpen))

	steps = append(steps, Step{Step: StepOpen, State: After(StepOpen), At: at(12)})
	st, err = Fold(steps)
	require.NoError(t, err)
	assert.Equal(t, Status{State: Running}, st, "open: the recovery is over")
	assert.NoError(t, Allowed(st, StepAnnounce), "another one may start")
}
