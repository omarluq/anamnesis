package journal

import (
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"
)

// Boots enumerates the distinct boots present in the journal, most recent first.
// It scans every record once, folding each into the realtime window of its
// _BOOT_ID so the returned BootInfo carries the earliest FirstSeen and latest
// LastSeen observed for that boot. Boots are ordered by descending start time, so
// the running boot leads the slice with Index 0 and older boots follow with
// decreasing negative indexes (-1, -2, ...), mirroring journalctl --list-boots.
// Every BootInfo satisfies FirstSeen <= LastSeen. Boots borrows a pooled Reader
// for the call and releases it before returning.
func (client *Client) Boots() ([]BootInfo, error) {
	return withReader(client, "boots", enumerateBoots)
}

// enumerateBoots seeks reader to the head, scans every record into per-boot
// windows and returns them ordered most recent first, wrapping any seek failure
// with journal context.
func enumerateBoots(reader Reader) ([]BootInfo, error) {
	if err := seekHead(reader); err != nil {
		return nil, err
	}

	windows, err := scanBootWindows(reader)
	if err != nil {
		return nil, err
	}

	return orderBoots(windows), nil
}

// bootWindow accumulates the realtime span of a single boot as its records are
// scanned: firstSeen tracks the earliest timestamp observed and lastSeen the
// latest, both keyed by the boot's id.
type bootWindow struct {
	firstSeen time.Time
	lastSeen  time.Time
	id        string
}

// scanBootWindows walks reader from the current position, decoding each record and
// folding it into the window of its _BOOT_ID, until the journal is exhausted. It
// returns the windows keyed by boot id.
func scanBootWindows(reader Reader) (map[string]*bootWindow, error) {
	windows := make(map[string]*bootWindow)

	err := forEachRecord(reader, func(fields map[string]any) {
		entry := parseEntry(fields)
		mergeBoot(windows, &entry)
	})
	if err != nil {
		return nil, err
	}

	return windows, nil
}

// mergeBoot folds entry into the window of its boot, opening a fresh window the
// first time a boot is seen and otherwise widening the existing window to include
// the entry's timestamp. A record without a _BOOT_ID is skipped rather than folded
// into a phantom boot keyed by the empty string, matching the skip-empty convention
// in counts.go.
func mergeBoot(windows map[string]*bootWindow, entry *Entry) {
	if entry.BootID == "" {
		return
	}

	window, seen := windows[entry.BootID]
	if !seen {
		windows[entry.BootID] = &bootWindow{
			firstSeen: entry.Timestamp,
			lastSeen:  entry.Timestamp,
			id:        entry.BootID,
		}

		return
	}

	if entry.Timestamp.Before(window.firstSeen) {
		window.firstSeen = entry.Timestamp
	}

	if entry.Timestamp.After(window.lastSeen) {
		window.lastSeen = entry.Timestamp
	}
}

// orderBoots sorts the windows by descending start time — the running boot first —
// and projects them into BootInfo values whose Index counts down from 0 for the
// running boot to decreasing negatives for older boots. Boots that start at the
// same instant fall back to a descending id comparison so the order is stable.
func orderBoots(windows map[string]*bootWindow) []BootInfo {
	ordered := lo.Values(windows)

	slices.SortFunc(ordered, func(left, right *bootWindow) int {
		if byStart := right.firstSeen.Compare(left.firstSeen); byStart != 0 {
			return byStart
		}

		return strings.Compare(right.id, left.id)
	})

	return lo.Map(ordered, func(window *bootWindow, index int) BootInfo {
		return BootInfo{
			FirstSeen: window.firstSeen,
			LastSeen:  window.lastSeen,
			ID:        window.id,
			Index:     -index,
		}
	})
}
