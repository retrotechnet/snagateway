package d3270

import (
	"fmt"
	"strings"
)

// Screen is a minimal 3270 display buffer. It exists primarily so the TN3270
// back end can be validated by eye: feed it inbound write data streams and
// Render() the result. It implements the common subset of orders; it is not a
// full RFC 3270 device.
type Screen struct {
	Rows, Cols int
	buf        []byte // EBCDIC display characters, len Rows*Cols
	attrs      []byte // field attribute at each start-field position; 0 = not a field start
	cursor     int
}

// NewScreen creates a cleared screen for the given model.
func NewScreen(model string) *Screen {
	r, c := Geometry(model)
	s := &Screen{Rows: r, Cols: c}
	s.buf = make([]byte, r*c)
	s.attrs = make([]byte, r*c)
	s.clear()
	return s
}

func (s *Screen) clear() {
	for i := range s.buf {
		s.buf[i] = 0x40 // EBCDIC space
		s.attrs[i] = 0
	}
	s.cursor = 0
}

func (s *Screen) size() int { return s.Rows * s.Cols }

// Apply parses one inbound write data stream (command byte + WCC + orders/data)
// and updates the screen. Unknown commands are ignored.
func (s *Screen) Apply(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	cmd := data[0]
	switch cmd {
	case CmdEW, CmdEWA:
		s.clear()
	case CmdW:
		// keep existing buffer
	case CmdWSF:
		// Structured fields (queries, etc.) — not rendered here.
		return nil
	case CmdEAU:
		for i := range s.buf {
			s.buf[i] = 0x40
		}
		return nil
	case CmdRB, CmdRM, CmdRMA, CmdNOP:
		return nil
	default:
		return fmt.Errorf("d3270: unhandled command 0x%02X", cmd)
	}

	i := 1
	if i < len(data) {
		i++ // skip WCC
	}
	addr := 0
	for i < len(data) {
		b := data[i]
		switch b {
		case OrderSBA:
			if i+2 >= len(data) {
				return nil
			}
			addr = DecodeAddr(data[i+1], data[i+2])
			i += 3
		case OrderSF:
			if i+1 >= len(data) {
				return nil
			}
			attr := data[i+1]
			s.attrs[s.wrap(addr)] = attr | 0x80 // mark as field start (0x80 internal flag)
			s.buf[s.wrap(addr)] = 0x40
			addr = s.inc(addr)
			i += 2
		case OrderSFE:
			if i+1 >= len(data) {
				return nil
			}
			npairs := int(data[i+1])
			i += 2
			var basic byte
			for p := 0; p < npairs && i+1 < len(data); p++ {
				typ, val := data[i], data[i+1]
				if typ == 0xC0 { // basic field attribute
					basic = val
				}
				i += 2
			}
			s.attrs[s.wrap(addr)] = basic | 0x80
			s.buf[s.wrap(addr)] = 0x40
			addr = s.inc(addr)
		case OrderSA:
			i += 3 // attribute type + value (ignored for rendering)
		case OrderMF:
			if i+1 >= len(data) {
				return nil
			}
			npairs := int(data[i+1])
			i += 2 + npairs*2
		case OrderIC:
			s.cursor = s.wrap(addr)
			i++
		case OrderPT:
			i++
		case OrderRA:
			if i+3 >= len(data) {
				return nil
			}
			stop := DecodeAddr(data[i+1], data[i+2])
			ch := data[i+3]
			for addr != stop {
				s.buf[s.wrap(addr)] = ch
				addr = s.inc(addr)
			}
			i += 4
		case OrderEUA:
			if i+2 >= len(data) {
				return nil
			}
			stop := DecodeAddr(data[i+1], data[i+2])
			for addr != stop {
				s.buf[s.wrap(addr)] = 0x40
				addr = s.inc(addr)
			}
			i += 3
		case OrderGE:
			i += 2 // graphic escape + char (rendered as the char)
			if i-1 < len(data) {
				s.buf[s.wrap(addr)] = data[i-1]
				addr = s.inc(addr)
			}
		default:
			// Plain display character.
			s.buf[s.wrap(addr)] = b
			addr = s.inc(addr)
			i++
		}
	}
	return nil
}

func (s *Screen) wrap(a int) int {
	n := s.size()
	a %= n
	if a < 0 {
		a += n
	}
	return a
}

func (s *Screen) inc(a int) int { return s.wrap(a + 1) }

// Render returns the screen as a box-bordered grid of ASCII text. Field-start
// positions render as spaces (the attribute byte is not a display character).
func (s *Screen) Render() string {
	var sb strings.Builder
	sb.WriteString("+" + strings.Repeat("-", s.Cols) + "+\n")
	for r := 0; r < s.Rows; r++ {
		sb.WriteByte('|')
		for c := 0; c < s.Cols; c++ {
			idx := r*s.Cols + c
			if s.attrs[idx]&0x80 != 0 {
				sb.WriteByte(' ') // field attribute cell
				continue
			}
			ch := ebcdicToASCII[s.buf[idx]]
			if ch < 0x20 || ch > 0x7E {
				ch = ' '
			}
			sb.WriteByte(ch)
		}
		sb.WriteString("|\n")
	}
	sb.WriteString("+" + strings.Repeat("-", s.Cols) + "+\n")
	return sb.String()
}
