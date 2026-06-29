package repl

// The host-read progress decorators that lived here were reverted: bumping the idle
// watchdog's progress counter on every journal.*/systemd.* read immunized a host-read
// loop (and a single pathological full-journal scan) from the watchdog, turning a slow
// query into an unbounded hang. The real fix is making journal.Query seek to its window
// instead of scanning from head — see internal/ana/journal/query.go. This file is now
// empty and can be removed.
