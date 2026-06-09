package sna

import (
	"sync"

	"snagateway/internal/d3270"
	"snagateway/internal/llc2"
)

// LU2Session is a concrete active dependent-LU2 (3270 display) session over an
// LLC2 link. It satisfies LUSession, so internal/bridge can couple it to a
// tn3270.Client. The owning read loop calls Deliver to feed inbound terminal
// data into FromTerminal.
type LU2Session struct {
	conn      llc2.Conn
	lu        byte // SLU local address
	plu       byte // PLU/host local address (LU-LU session OAF)
	odai      bool // LU-LU session ODAI
	viaSSCPLU bool          // send over the SSCP-LU session (USS mode) instead of LU-LU
	screen    *d3270.Screen // SSCP-LU: render buffer (the applet ignores positioning orders)

	mu        sync.Mutex
	snf       uint16
	firstSend bool

	inbound chan []byte
	closed  chan struct{}
	once    sync.Once
}

// OnSSCPLUSend, if set, is a debug hook invoked for each screen relayed over the
// SSCP-LU session with the original and flattened 3270 data streams.
var OnSSCPLUSend func(original, flattened []byte)

// NewLUSession creates an active LU2 session bound to conn with the given LU-LU
// addressing (lu = SLU local address, plu = PLU/host address, odai = ODAI bit).
func NewLUSession(conn llc2.Conn, lu, plu byte, odai bool) *LU2Session {
	return &LU2Session{
		conn:      conn,
		lu:        lu,
		plu:       plu,
		odai:      odai,
		firstSend: true,
		inbound:   make(chan []byte, 16),
		closed:    make(chan struct{}),
	}
}

// NewSSCPLUSession creates a session that carries 3270 data over the SSCP-LU
// session (USS mode) instead of a bound LU-LU session — letting screens reach a
// dependent terminal without a BIND. Input arrives on the same SSCP-LU session.
// model sizes the render buffer (e.g. "IBM-3278-2" = 24x80).
func NewSSCPLUSession(conn llc2.Conn, lu byte, model string) *LU2Session {
	s := NewLUSession(conn, lu, 0, false)
	s.viaSSCPLU = true
	s.screen = d3270.NewScreen(model)
	return s
}

// LU returns the SLU local address.
func (s *LU2Session) LU() byte { return s.lu }

// SendToTerminal writes an outbound 3270 data stream (host->terminal) as an FMD
// RU on the LU-LU session. The first write of a bracket sets BBI; every write
// hands the turn to the terminal (change-direction) so it can send input.
func (s *LU2Session) SendToTerminal(ds []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.viaSSCPLU {
		return s.sendSSCPLU(ds)
	}
	s.snf++
	begin := s.firstSend
	s.firstSend = false
	return s.conn.Write(BuildFMD(s.lu, s.plu, s.odai, s.snf, ds, begin))
}

// maxSSCPLUData bounds the 3270 data per PIU to stay under SNA Server's BTU
// (1493); a continuation Write (no SBA) resumes filling where the prior left off.
const maxSSCPLUData = 1400

// sendSSCPLU renders the host 3270 write into the session's screen buffer (which
// correctly applies all positioning/field orders), then re-emits the buffer as a
// linear character stream — no SBA/SF orders, which the SSCP-LU applet cannot
// process. Large screens are split across continuation Writes.
func (s *LU2Session) sendSSCPLU(ds []byte) error {
	_ = s.screen.Apply(ds)
	linear := s.screen.Linear()
	if OnSSCPLUSend != nil {
		OnSSCPLUSend(ds, linear)
	}
	for off := 0; ; {
		end := off + maxSSCPLUData
		if end > len(linear) {
			end = len(linear)
		}
		cmd := byte(d3270.CmdW) // continuation: keep filling from current address
		if off == 0 {
			cmd = d3270.CmdEW // first chunk erases and starts at 0
		}
		out := append([]byte{cmd, 0xC3}, linear[off:end]...) // WCC: reset MDT + restore keyboard
		s.snf++
		if err := s.conn.Write(BuildSSCPLUData(s.lu, s.snf, out)); err != nil {
			return err
		}
		off = end
		if off >= len(linear) {
			return nil
		}
	}
}

// FromTerminal yields inbound terminal->host 3270 data streams.
func (s *LU2Session) FromTerminal() <-chan []byte { return s.inbound }

// Closed is signaled when the session ends.
func (s *LU2Session) Closed() <-chan struct{} { return s.closed }

// Close ends the session.
func (s *LU2Session) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// Deliver feeds an inbound terminal->host 3270 data stream (AID + modified
// fields) to FromTerminal. Called by the read loop for LU-LU FMD RUs.
func (s *LU2Session) Deliver(ds []byte) {
	select {
	case s.inbound <- ds:
	case <-s.closed:
	}
}

// BuildSSCPLUData builds an FMD PIU carrying data on the SSCP-LU session
// (DAF=lu, OAF=0, ODAI=0, normal flow) — e.g. a USS-style 3270 logon/display
// screen the SSCP sends to a dependent LU before any LU-LU session exists.
func BuildSSCPLUData(lu byte, snf uint16, data []byte) []byte {
	th := TH{MPF: MPFWhole, ODAI: false, EFI: false, DAF: lu, OAF: 0x00, SNF: snf}
	rh := RH{Category: CategoryFMD, BCI: true, ECI: true, DR1: true}
	return BuildPIU(th, rh, data)
}

// BuildFMD builds an FMD PIU carrying a 3270 data stream on the LU-LU session
// (normal flow). beginBracket sets BBI for the first RU of a bracket; CDI is set
// so the terminal may send its response.
func BuildFMD(lu, plu byte, odai bool, snf uint16, data []byte, beginBracket bool) []byte {
	th := TH{MPF: MPFWhole, ODAI: odai, EFI: false, DAF: lu, OAF: plu, SNF: snf}
	rh := RH{Category: CategoryFMD, BCI: true, ECI: true, DR1: true, CDI: true, BBI: beginBracket}
	return BuildPIU(th, rh, data)
}

// Ensure *LU2Session satisfies the LUSession interface.
var _ LUSession = (*LU2Session)(nil)
