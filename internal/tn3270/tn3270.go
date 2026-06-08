// Package tn3270 is a TN3270 client: it connects to a TN3270 host (Hercules,
// Sim390, or any TN3270/TCP system), performs telnet option negotiation
// (RFC 1576 basic mode, with best-effort TN3270E / RFC 2355), and exchanges
// 3270 data-stream records.
//
// This is the gateway's back end. It is fully usable on its own, which is why
// the `snagateway tn3270` subcommand can validate a host before any SNA code
// exists.
package tn3270

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

// Options configures a TN3270 client connection.
type Options struct {
	Addr     string        // host:port
	TermType string        // e.g. "IBM-3278-2"
	TN3270E  bool          // attempt TN3270E negotiation
	Timeout  time.Duration // dial timeout (0 = 30s)
	Logger   *log.Logger   // optional; nil disables protocol logging
}

// Client is a connected TN3270 session.
type Client struct {
	opts    Options
	conn    net.Conn
	r       *bufio.Reader
	tn3270e bool // negotiated TN3270E successfully
	log     *log.Logger

	// Telnet option agreement, tracked to detect when 3270 data mode is fully
	// established (basic TN3270 requires BINARY+EOR in both directions).
	binIn, binOut bool
	eorIn, eorOut bool
	mode3270      bool
}

// Dial connects and performs telnet negotiation, returning a ready client.
func Dial(opts Options) (*Client, error) {
	if opts.TermType == "" {
		opts.TermType = "IBM-3278-2"
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	conn, err := net.DialTimeout("tcp", opts.Addr, opts.Timeout)
	if err != nil {
		return nil, err
	}
	c := &Client{opts: opts, conn: conn, r: bufio.NewReader(conn), log: opts.Logger}
	c.logf("connected to %s", opts.Addr)
	return c, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// TN3270E reports whether TN3270E mode was negotiated.
func (c *Client) TN3270E() bool { return c.tn3270e }

func (c *Client) logf(format string, args ...any) {
	if c.log != nil {
		c.log.Printf("tn3270: "+format, args...)
	}
}

// ReadRecord reads the next inbound 3270 data-stream record, performing any
// telnet negotiation encountered along the way. In TN3270E mode the 5-byte
// data-message header is stripped and only 3270-data records are returned
// (other message types are logged and skipped).
func (c *Client) ReadRecord() ([]byte, error) {
	for {
		rec, err := c.readRawRecord()
		if err != nil {
			return nil, err
		}
		if !c.tn3270e {
			return rec, nil
		}
		if len(rec) < 5 {
			c.logf("short TN3270E record (%d bytes), skipping", len(rec))
			continue
		}
		dataType := rec[0]
		payload := rec[5:]
		switch dataType {
		case dt3270Data, dtSSCPLUData:
			return payload, nil
		case dtBindImage, dtUnbind, dtResponse, dtNVTData:
			c.logf("TN3270E control record type 0x%02X (%d bytes), skipping", dataType, len(payload))
		default:
			c.logf("TN3270E record type 0x%02X, skipping", dataType)
		}
	}
}

// readRawRecord reads bytes until IAC EOR, handling telnet commands inline and
// returning the assembled data (IAC IAC un-escaped).
func (c *Client) readRawRecord() ([]byte, error) {
	var rec []byte
	for {
		b, err := c.r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b != iacIAC {
			rec = append(rec, b)
			continue
		}
		// IAC seen.
		cmd, err := c.r.ReadByte()
		if err != nil {
			return nil, err
		}
		switch cmd {
		case iacIAC:
			rec = append(rec, iacIAC)
		case iacEOR:
			c.logf("inbound record: %d bytes", len(rec))
			return rec, nil
		case iacDO, iacDONT, iacWILL, iacWONT:
			opt, err := c.r.ReadByte()
			if err != nil {
				return nil, err
			}
			if err := c.handleNegotiation(cmd, opt); err != nil {
				return nil, err
			}
		case iacSB:
			if err := c.handleSubnegotiation(); err != nil {
				return nil, err
			}
		default:
			// Other telnet commands (NOP, etc.) — ignore.
		}
	}
}

func (c *Client) handleNegotiation(cmd, opt byte) error {
	c.logf("recv IAC %s %s", cmdName(cmd), optName(opt))
	switch opt {
	case optTTYPE:
		if cmd == iacDO {
			return c.reply(iacWILL, optTTYPE)
		}
	case optEOR:
		if cmd == iacDO { // host wants us to mark records with EOR (outbound)
			c.eorOut = true
			defer c.check3270Mode()
			return c.reply(iacWILL, optEOR)
		}
		if cmd == iacWILL { // host will mark inbound records with EOR
			c.eorIn = true
			defer c.check3270Mode()
			return c.reply(iacDO, optEOR)
		}
	case optBINARY:
		if cmd == iacDO { // host wants 8-bit clean outbound
			c.binOut = true
			defer c.check3270Mode()
			return c.reply(iacWILL, optBINARY)
		}
		if cmd == iacWILL { // host will send 8-bit clean inbound
			c.binIn = true
			defer c.check3270Mode()
			return c.reply(iacDO, optBINARY)
		}
	case optTN3270E:
		if cmd == iacDO {
			if c.opts.TN3270E {
				c.tn3270e = true
				return c.reply(iacWILL, optTN3270E)
			}
			return c.reply(iacWONT, optTN3270E)
		}
		if cmd == iacWILL {
			if c.opts.TN3270E {
				c.tn3270e = true
				return c.reply(iacDO, optTN3270E)
			}
			return c.reply(iacDONT, optTN3270E)
		}
	default:
		// Refuse anything we don't implement.
		switch cmd {
		case iacWILL:
			return c.reply(iacDONT, opt)
		case iacDO:
			return c.reply(iacWONT, opt)
		}
	}
	return nil
}

// reply sends a 3-byte IAC option response and logs it.
func (c *Client) reply(cmd, opt byte) error {
	c.logf("send IAC %s %s", cmdName(cmd), optName(opt))
	return c.send(iacIAC, cmd, opt)
}

// check3270Mode logs once when BINARY+EOR are agreed in both directions, which
// is the point at which the host may begin sending the 3270 data stream. If you
// see this line and then nothing, the gateway negotiated fine and is waiting on
// the host to write a screen (e.g. no OS/VTAM is driving that device).
func (c *Client) check3270Mode() {
	if c.mode3270 || !(c.binIn && c.binOut && c.eorIn && c.eorOut) {
		return
	}
	c.mode3270 = true
	c.logf("3270 data mode active (BINARY+EOR both directions) — awaiting host write")
}

func cmdName(b byte) string {
	switch b {
	case iacDO:
		return "DO"
	case iacDONT:
		return "DONT"
	case iacWILL:
		return "WILL"
	case iacWONT:
		return "WONT"
	case iacSB:
		return "SB"
	default:
		return fmt.Sprintf("CMD(%d)", b)
	}
}

func optName(b byte) string {
	switch b {
	case optBINARY:
		return "BINARY"
	case optEOR:
		return "EOR"
	case optTTYPE:
		return "TERMINAL-TYPE"
	case optTN3270E:
		return "TN3270E"
	default:
		return fmt.Sprintf("OPT(%d)", b)
	}
}

func (c *Client) handleSubnegotiation() error {
	// Read up to IAC SE.
	var sub []byte
	for {
		b, err := c.r.ReadByte()
		if err != nil {
			return err
		}
		if b == iacIAC {
			n, err := c.r.ReadByte()
			if err != nil {
				return err
			}
			if n == iacSE {
				break
			}
			sub = append(sub, n) // IAC IAC -> literal
			continue
		}
		sub = append(sub, b)
	}
	if len(sub) == 0 {
		return nil
	}
	switch sub[0] {
	case optTTYPE:
		if len(sub) >= 2 && sub[1] == ttypeSEND {
			return c.sendTermType()
		}
	case optTN3270E:
		return c.handleTN3270ESub(sub[1:])
	}
	return nil
}

func (c *Client) sendTermType() error {
	c.logf("sending terminal type %q", c.opts.TermType)
	out := []byte{iacIAC, iacSB, optTTYPE, ttypeIS}
	out = append(out, []byte(c.opts.TermType)...)
	out = append(out, iacIAC, iacSE)
	return c.sendBytes(out)
}

// handleTN3270ESub implements the minimal DEVICE-TYPE / FUNCTIONS exchange.
func (c *Client) handleTN3270ESub(sub []byte) error {
	if len(sub) == 0 {
		return nil
	}
	switch sub[0] {
	case tn3270eSEND:
		// Host asks us to SEND DEVICE-TYPE: reply REQUEST our device type.
		out := []byte{iacIAC, iacSB, optTN3270E, tn3270eDEVICE_TYPE, tn3270eREQUEST}
		out = append(out, []byte(c.opts.TermType)...)
		out = append(out, iacIAC, iacSE)
		c.logf("TN3270E: requesting device type %q", c.opts.TermType)
		return c.sendBytes(out)
	case tn3270eDEVICE_TYPE:
		if len(sub) >= 2 && sub[1] == tn3270eIS {
			c.logf("TN3270E: host assigned device type/name")
		}
		// Acknowledge with empty FUNCTIONS REQUEST (no extra functions).
		out := []byte{iacIAC, iacSB, optTN3270E, tn3270eFUNCTIONS, tn3270eREQUEST, iacIAC, iacSE}
		return c.sendBytes(out)
	case tn3270eFUNCTIONS:
		c.logf("TN3270E: functions negotiated")
	}
	return nil
}

// WriteRecord sends a 3270 data-stream record (IAC-escaped, IAC EOR
// terminated). In TN3270E mode it prepends a 3270-DATA message header.
func (c *Client) WriteRecord(data []byte) error {
	var out []byte
	if c.tn3270e {
		// 5-byte header: DATA-TYPE, REQUEST-FLAG, RESPONSE-FLAG, SEQ(2).
		out = append(out, dt3270Data, 0x00, 0x00, 0x00, 0x00)
	}
	for _, b := range data {
		if b == iacIAC {
			out = append(out, iacIAC, iacIAC) // escape
		} else {
			out = append(out, b)
		}
	}
	out = append(out, iacIAC, iacEOR)
	return c.sendBytes(out)
}

func (c *Client) send(b ...byte) error { return c.sendBytes(b) }

func (c *Client) sendBytes(b []byte) error {
	_, err := c.conn.Write(b)
	if err != nil {
		return fmt.Errorf("tn3270 write: %w", err)
	}
	return nil
}

// Conn exposes the underlying connection (e.g. to set deadlines).
func (c *Client) Conn() net.Conn { return c.conn }

var _ io.Closer = (*Client)(nil)
