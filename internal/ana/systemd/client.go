package systemd

import (
	"context"
	"sync"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/samber/lo"
	"github.com/samber/oops"
)

// Client reads systemd unit state over the system D-Bus connection. The
// connection is dialed lazily on the first read and reused for the Client's life,
// so constructing a Client opens no bus connection and a Client assembles even
// where the system bus is unreachable — the dial, and any failure it raises, is
// deferred to the first call rather than to construction. The zero value is
// usable and dials the real system bus on first read; NewClient is the
// conventional constructor. A Client is safe for concurrent use by multiple
// goroutines and must not be copied after first use.
type Client struct {
	// conn is the cached systemd D-Bus connection, nil until the first read; the
	// mutex below guards it so the bus is dialed once.
	conn *dbus.Conn
	// dial opens the system D-Bus connection; the field is a seam so a
	// test can inject a connection without a live bus.
	dial func(ctx context.Context) (*dbus.Conn, error)
	// mutex guards the lazily dialed connection so the bus is dialed once.
	mutex sync.Mutex
}

// NewClient builds a systemd Client that dials the private systemd D-Bus
// connection lazily on first use. Construction performs no I/O, mirroring the
// pooled journal Client: the bus connection, and any failure to open it, is
// deferred to the first ListUnits or UnitStatus call.
func NewClient() *Client {
	return &Client{
		conn:  nil,
		dial:  dbus.NewSystemConnectionContext,
		mutex: sync.Mutex{},
	}
}

// ListUnits lists the loaded units whose state matches state, or every loaded
// unit when state is empty. It dials the bus on first use and projects each D-Bus
// unit record into the systemd.Unit value type the host surface exposes. It
// returns an oops error tagged with the systemd domain when the bus cannot be
// dialed or the listing call fails.
func (client *Client) ListUnits(ctx context.Context, state string) ([]Unit, error) {
	conn, err := client.connection(ctx)
	if err != nil {
		return nil, err
	}

	statuses, err := listStatuses(ctx, conn, state)
	if err != nil {
		return nil, err
	}

	return lo.Map(statuses, func(status dbus.UnitStatus, _ int) Unit {
		return Unit{
			Name:        status.Name,
			Description: status.Description,
			LoadState:   status.LoadState,
			ActiveState: status.ActiveState,
			SubState:    status.SubState,
		}
	}), nil
}

// UnitStatus returns the detailed status of the named unit, dialing the bus on
// first use. The listing fields come from the unit record and MainPID is read
// best-effort from the unit's Service property — units with no main process, or
// non-service units, report a zero MainPID. An unknown or not-loaded unit yields a
// not-found status (LoadState "not-found") with no error, so the caller can tell it
// apart from a real unit with blank state; a bus or listing failure surfaces as an
// oops error tagged with the systemd domain.
func (client *Client) UnitStatus(ctx context.Context, name string) (UnitStatus, error) {
	var empty UnitStatus

	conn, err := client.connection(ctx)
	if err != nil {
		return empty, err
	}

	statuses, err := conn.ListUnitsByNamesContext(ctx, []string{name})
	if err != nil {
		return empty, oops.In("systemd").Code("unit_status").Wrapf(err, "read status of unit %q", name)
	}

	status, found := lo.Find(statuses, func(candidate dbus.UnitStatus) bool {
		return candidate.Name == name
	})
	// ListUnitsByNames returns a placeholder record with empty fields for a name that
	// is not a currently-loaded unit — a wrong name, an alias that is not the loaded
	// name, or a genuinely absent unit. Report that as a "not-found" LoadState so the
	// controller can tell "no such unit" apart from a real unit with blank state and
	// re-query systemd.ListUnits for the exact loaded name.
	if !found || status.LoadState == "" {
		return UnitStatus{
			Name:        name,
			Description: "",
			LoadState:   "not-found",
			ActiveState: "",
			SubState:    "",
			MainPID:     0,
		}, nil
	}

	return UnitStatus{
		Name:        status.Name,
		Description: status.Description,
		LoadState:   status.LoadState,
		ActiveState: status.ActiveState,
		SubState:    status.SubState,
		MainPID:     mainPID(ctx, conn, name),
	}, nil
}

// connection returns the Client's D-Bus connection, dialing and caching it on the
// first call. Concurrent callers serialize on the mutex so the bus is dialed once,
// and a dial failure surfaces as an oops error tagged with the systemd domain.
func (client *Client) connection(ctx context.Context) (*dbus.Conn, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()

	if client.conn != nil {
		return client.conn, nil
	}

	dial := client.dial
	if dial == nil {
		dial = dbus.NewSystemConnectionContext
	}

	conn, err := dial(ctx)
	if err != nil {
		return nil, oops.In("systemd").Code("dial").Wrapf(err, "dial systemd dbus connection")
	}

	client.conn = conn

	return conn, nil
}

// listStatuses fetches the raw D-Bus unit listing, restricting it to the named
// state when one is given and returning the full loaded set otherwise. It wraps
// either listing failure in an oops error tagged with the systemd domain.
func listStatuses(ctx context.Context, conn *dbus.Conn, state string) ([]dbus.UnitStatus, error) {
	if state == "" {
		statuses, err := conn.ListUnitsContext(ctx)
		if err != nil {
			return nil, oops.In("systemd").Code("list_units").Wrapf(err, "list systemd units")
		}

		return statuses, nil
	}

	statuses, err := conn.ListUnitsFilteredContext(ctx, []string{state})
	if err != nil {
		return nil, oops.In("systemd").Code("list_units").Wrapf(err, "list systemd units in state %q", state)
	}

	return statuses, nil
}

// mainPID reads the unit's main process identifier from its Service property,
// returning zero when the property is unavailable — the unit is not a service, has
// no running main process, or the read fails — so a missing PID never faults a
// status read.
func mainPID(ctx context.Context, conn *dbus.Conn, name string) int {
	property, err := conn.GetServicePropertyContext(ctx, name, "MainPID")
	if err != nil {
		return 0
	}

	pid, ok := property.Value.Value().(uint32)
	if !ok {
		return 0
	}

	return int(pid)
}
