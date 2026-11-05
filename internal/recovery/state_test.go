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
