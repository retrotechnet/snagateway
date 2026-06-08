package sna

import (
	"errors"

	"snagateway/internal/llc2"
)

// ErrNotImplemented marks the parts of the SSCP/PU5 state machine still to be
// built (phase 4).
var ErrNotImplemented = errors.New("sna: SSCP/PU5 session handling not yet implemented")

// LUSession is one active dependent-LU2 (3270 display) session as seen from the
// SNA side. internal/bridge couples it to a tn3270.Client.
//
// Data-flow directions (the gateway sits between an SNA "terminal" on SNA
// Server and a TN3270 "host" on Hercules/Sim390):
//
//	host -> terminal : SendToTerminal(ds)  -- TN3270 ReadRecord feeds this
//	terminal -> host : <-FromTerminal()    -- forwarded to TN3270 WriteRecord
type LUSession interface {
	// LU is the local LU address on the PU.
	LU() byte

	// SendToTerminal delivers an outbound 3270 data stream (host->terminal) to
	// SNA Server as one or more FMD RUs (with chaining/SDT as required).
	SendToTerminal(dataStream []byte) error

	// FromTerminal yields inbound 3270 data streams (terminal->host): the AID
	// byte, cursor address, and modified fields produced when the user presses
	// Enter/PF/PA. The bridge forwards each to the TN3270 host.
	FromTerminal() <-chan []byte

	// Closed is signaled when the LU-LU session ends (UNBIND/DACTLU/link loss).
	Closed() <-chan struct{}

	Close() error
}

// SessionHandler is invoked when an LU-LU session reaches "data traffic active"
// (post-SDT) and is ready to bridge. The handler typically dials the TN3270
// back end and starts internal/bridge.
type SessionHandler func(LUSession)

// Manager runs the host-side SNA over a single LLC2 link to one SNA Server PU.
// It owns the SSCP/PU5 logic: activate the PU, activate each configured LU,
// drive BIND/SDT, and surface active sessions to a SessionHandler.
type Manager struct {
	conn    llc2.Conn
	lus     []byte // LU local addresses to activate
	handler SessionHandler
}

// NewManager creates a session manager for one LLC2 connection.
func NewManager(conn llc2.Conn, lus []byte, handler SessionHandler) *Manager {
	return &Manager{conn: conn, lus: lus, handler: handler}
}

// Run drives the link until it closes. Outline of the state machine to build:
//
//	1. XID/contact is handled by the kernel LLC2 layer; the first PIU we expect
//	   is SNA Server reacting to our ACTPU. As the host (SSCP/PU5) we initiate:
//	     - send ACTPU(ERP) to the PU (DAF = PU, SC category, RU=0x11)
//	     - on +RSP(ACTPU), send ACTLU to each LU in m.lus (RU=0x0D)
//	     - on +RSP(ACTLU), the LU is "active" (SSCP-LU session up)
//	   2. When SNA Server / the downstream 3270 client requests a session
//	      (INITIATE-SELF / NOTIFY, or implicitly), send BIND (RU=0x31) with an
//	      LU2 BIND image (FM/TS profiles 3/4, 3270 presentation, screen size
//	      from the model), then SDT (RU=0xA0) to start data traffic.
//	   3. On +RSP(SDT): build an luSession and call m.handler(session).
//	   4. Thereafter: FMD RUs carry the 3270 data stream. Handle chaining
//	      (BCI/ECI), bracket protocol (BBI/EBI/CDI for half-duplex flip-flop),
//	      and definite/exception responses. Map terminal AID input to
//	      FromTerminal(); map SendToTerminal() to outbound FMD RUs.
//	   5. UNBIND (0x32) / DACTLU (0x0E) / DACTPU (0x12) tear sessions down.
//
// Each step has a well-defined RU and response; SenseInvalidFormat and friends
// (see sna.go) cover the negative-response paths.
func (m *Manager) Run() error {
	// TODO(phase 4): implement the state machine described above, reading PIUs
	// via m.conn.Read() and writing via m.conn.Write(BuildPIU(...)).
	return ErrNotImplemented
}
