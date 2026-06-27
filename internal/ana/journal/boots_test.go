package journal_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// bootFirst is the _BOOT_ID of the oldest boot in the fixture; bootSecond (the
// middle boot) and bootCurrent (the running boot) are declared in the sibling
// query and fakereader tests. distinctBootCount also comes from fakereader_test.go.
const bootFirst = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"

// Realtime bounds of the running boot (bootCurrent) in the fixture in microseconds,
// used to prove Boots folds a boot's earliest and latest record into one window.
const (
	currentFirstMicros = 1782468000000000
	currentLastMicros  = 1782468600000000
)

// loadBoots builds a fixture-backed client, enumerates its boots and returns them,
// registering the client's Close as test cleanup so each case keeps only its own
// distinctive assertions. It fails the test if enumerating the boots errors.
func loadBoots(t *testing.T) []journal.BootInfo {
	t.Helper()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})

	boots, err := client.Boots()
	require.NoError(t, err)

	return boots
}

func TestBootsEnumeratesDistinctBootsMostRecentFirst(t *testing.T) {
	t.Parallel()

	boots := loadBoots(t)
	require.Len(t, boots, distinctBootCount)

	assert.Equal(t, bootCurrent, boots[0].ID)
	assert.Equal(t, bootSecond, boots[1].ID)
	assert.Equal(t, bootFirst, boots[2].ID)
}

func TestBootsRunningBootIsIndexZeroWithDecreasingNegatives(t *testing.T) {
	t.Parallel()

	boots := loadBoots(t)
	require.Len(t, boots, distinctBootCount)

	assert.Equal(t, 0, boots[0].Index)
	assert.Equal(t, -1, boots[1].Index)
	assert.Equal(t, -2, boots[2].Index)
}

func TestBootsOrderDescendsByStartWithDecreasingIndex(t *testing.T) {
	t.Parallel()

	boots := loadBoots(t)
	require.Len(t, boots, distinctBootCount)

	for index := 1; index < len(boots); index++ {
		newer := boots[index-1]
		older := boots[index]

		assert.Truef(t, newer.FirstSeen.After(older.FirstSeen),
			"boot %d must start after boot %d", index-1, index)
		assert.Equalf(t, newer.Index-1, older.Index, "boot indexes must decrease by one")
	}
}

func TestBootsFoldEarliestAndLatestRecordIntoWindow(t *testing.T) {
	t.Parallel()

	boots := loadBoots(t)
	require.Len(t, boots, distinctBootCount)

	running := boots[0]
	assert.Equal(t, bootCurrent, running.ID)
	assert.Equal(t, time.UnixMicro(currentFirstMicros).UTC(), running.FirstSeen)
	assert.Equal(t, time.UnixMicro(currentLastMicros).UTC(), running.LastSeen)
}

func TestBootsFirstSeenNotAfterLastSeen(t *testing.T) {
	t.Parallel()

	boots := loadBoots(t)

	for index := range boots {
		boot := boots[index]
		assert.Falsef(t, boot.FirstSeen.After(boot.LastSeen),
			"boot %s reports FirstSeen after LastSeen", boot.ID)
	}
}

func TestBootsPropagatesAcquireFailure(t *testing.T) {
	t.Parallel()

	factory := new(mockFactory)
	factory.On("NewReader").Return(nil, assert.AnError).Once()

	client := journal.NewClientWithFactory(factory, 1)

	boots, err := client.Boots()
	assert.Nil(t, boots)
	require.ErrorIs(t, err, assert.AnError)

	factory.AssertExpectations(t)
}

func TestBootsPropagatesSeekFailure(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("SeekHead").Return(assert.AnError)

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 1)

	boots, err := client.Boots()
	assert.Nil(t, boots)
	require.ErrorIs(t, err, assert.AnError)

	require.NoError(t, client.Close())
	reader.AssertExpectations(t)
	factory.AssertExpectations(t)
}

func TestBootsPropagatesAdvanceFailure(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(0), assert.AnError)

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 1)

	boots, err := client.Boots()
	assert.Nil(t, boots)
	require.ErrorIs(t, err, assert.AnError)

	require.NoError(t, client.Close())
	reader.AssertExpectations(t)
	factory.AssertExpectations(t)
}

func TestBootsSkipsRecordsWithoutBootID(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(1), nil).Once()
	reader.On("Next").Return(uint64(0), nil)
	reader.On("Fields").Return(map[string]any{
		journal.FieldCursor:   "synthetic-cursor",
		journal.FieldRealtime: "1782468000000000",
	}, nil).Once()

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 1)

	// The lone record carries no _BOOT_ID, so it must be skipped rather than
	// enumerated as a phantom boot keyed by the empty string.
	boots, err := client.Boots()
	require.NoError(t, err)
	assert.Empty(t, boots)

	require.NoError(t, client.Close())
	reader.AssertExpectations(t)
	factory.AssertExpectations(t)
}
