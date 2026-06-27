package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"

	"github.com/samber/oops"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// maxJournalLineBytes caps a single journalctl JSON record at 1 MiB so the
// scanner reads rows far larger than bufio's 64 KiB default — a long MESSAGE or
// a binary field encoded as a number array can easily exceed it — without
// failing on otherwise valid input.
const maxJournalLineBytes = 1 << 20

// ParseJournalExport reads a "journalctl --output=json" export — newline-
// delimited JSON, one record per line — from src and decodes each record into a
// journal.Entry via journal.DecodeFields. Blank lines are skipped.
// __REALTIME_TIMESTAMP is read as microseconds since the Unix epoch into a UTC
// time, PRIORITY and _PID are coerced from their string forms to ints, and any
// absent field (a missing _PID included) becomes its zero value without error. A
// line that is not valid JSON is reported as an oops-wrapped error naming its
// 1-based line number.
func ParseJournalExport(src io.Reader) ([]journal.Entry, error) {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxJournalLineBytes)

	var entries []journal.Entry

	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var fields map[string]any
		if decodeErr := json.Unmarshal(line, &fields); decodeErr != nil {
			return nil, oops.
				In("evals").
				Code("malformed_journal_line").
				Wrapf(decodeErr, "parse journal export on line %d", lineNum)
		}

		entries = append(entries, journal.DecodeFields(fields))
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return nil, oops.
			In("evals").
			Code("scan_journal_export").
			Wrapf(scanErr, "scan journal export")
	}

	return entries, nil
}
