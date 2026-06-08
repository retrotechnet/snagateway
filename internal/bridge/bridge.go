// Package bridge couples one SNA LU2 session (internal/sna) to one TN3270
// back-end connection (internal/tn3270), pumping 3270 data streams in both
// directions. Because the 3270 data stream is common to both transports, the
// bridge is intentionally thin: it moves records and translates only the
// envelope (which the two sides already handle), not the data stream itself.
package bridge

import (
	"errors"
	"io"
	"log"

	"snagateway/internal/sna"
	"snagateway/internal/tn3270"
)

// Bridge runs one LU2-session <-> TN3270 pairing.
type Bridge struct {
	session sna.LUSession
	host    *tn3270.Client
	log     *log.Logger
}

// New creates a bridge between an active SNA LU2 session and a connected
// TN3270 client.
func New(session sna.LUSession, host *tn3270.Client, logger *log.Logger) *Bridge {
	return &Bridge{session: session, host: host, log: logger}
}

func (b *Bridge) logf(format string, args ...any) {
	if b.log != nil {
		b.log.Printf("bridge[LU %d]: "+format, append([]any{b.session.LU()}, args...)...)
	}
}

// Run pumps both directions until either side closes. It returns when the first
// direction ends; the caller should Close both endpoints.
func (b *Bridge) Run() error {
	errc := make(chan error, 2)

	// host -> terminal: TN3270 host writes a 3270 data stream; deliver it to
	// the SNA terminal via the LU session.
	go func() {
		for {
			rec, err := b.host.ReadRecord()
			if err != nil {
				errc <- wrap("host read", err)
				return
			}
			if len(rec) == 0 {
				continue
			}
			if err := b.session.SendToTerminal(rec); err != nil {
				errc <- wrap("send to terminal", err)
				return
			}
			b.logf("host->terminal %d bytes (cmd 0x%02X)", len(rec), rec[0])
		}
	}()

	// terminal -> host: SNA terminal produced input (AID + modified fields);
	// forward it to the TN3270 host.
	go func() {
		in := b.session.FromTerminal()
		for {
			select {
			case rec, ok := <-in:
				if !ok {
					errc <- wrap("terminal input", io.EOF)
					return
				}
				if err := b.host.WriteRecord(rec); err != nil {
					errc <- wrap("host write", err)
					return
				}
				b.logf("terminal->host %d bytes (AID 0x%02X)", len(rec), firstByte(rec))
			case <-b.session.Closed():
				errc <- wrap("session", errSessionClosed)
				return
			}
		}
	}()

	return <-errc
}

var errSessionClosed = errors.New("lu session closed")

func wrap(where string, err error) error {
	if err == nil {
		return nil
	}
	return &bridgeError{where: where, err: err}
}

type bridgeError struct {
	where string
	err   error
}

func (e *bridgeError) Error() string { return "bridge: " + e.where + ": " + e.err.Error() }
func (e *bridgeError) Unwrap() error { return e.err }

func firstByte(b []byte) byte {
	if len(b) == 0 {
		return 0
	}
	return b[0]
}
