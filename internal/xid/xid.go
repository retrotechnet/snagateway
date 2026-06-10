// Package xid implements the SNA Type 2.1 XID3 link-activation exchange over raw
// Ethernet (AF_PACKET). The kernel AF_LLC stack only speaks generic 802.2 XID,
// not SNA's XID3 — and SNA Server (XID Format 3) requires the XID3 exchange to
// negotiate the address-assignment convention before it will accept LU-LU
// session BINDs. So we answer SNA Server's XID poll with a crafted XID3 here,
// then let the kernel AF_LLC socket handle the SABME/UA and data that follow.
package xid

import "net"

// LLCFrame is a parsed 802.3 (length-framed) Ethernet frame carrying an LLC PDU.
type LLCFrame struct {
	Dst, Src            net.HardwareAddr
	DSAP, SSAP, Control byte
	Info                []byte
}

// ParseLLCFrame parses an 802.3 Ethernet frame with an LLC header. ok is false
// for EtherType-II frames (length field > 1500), e.g. the DIX 0x80D5 copies.
func ParseLLCFrame(frame []byte) (LLCFrame, bool) {
	if len(frame) < 17 {
		return LLCFrame{}, false
	}
	length := int(frame[12])<<8 | int(frame[13])
	if length > 1500 {
		return LLCFrame{}, false
	}
	end := 14 + length
	if end > len(frame) {
		end = len(frame)
	}
	if end < 17 {
		return LLCFrame{}, false
	}
	return LLCFrame{
		Dst:     append(net.HardwareAddr{}, frame[0:6]...),
		Src:     append(net.HardwareAddr{}, frame[6:12]...),
		DSAP:    frame[14],
		SSAP:    frame[15],
		Control: frame[16],
		Info:    append([]byte{}, frame[17:end]...),
	}, true
}

// IsXID reports whether an LLC U-frame control byte is XID (0xAF/0xBF, P/F masked).
func IsXID(control byte) bool { return control&0xEF == 0xAF }

// IsSABME reports whether the control byte is SABME (0x6F/0x7F).
func IsSABME(control byte) bool { return control&0xEF == 0x6F }

// ControlName gives a short label for common U-frame control bytes.
func ControlName(c byte) string {
	switch {
	case IsXID(c):
		return "XID"
	case IsSABME(c):
		return "SABME"
	case c&0xEF == 0x63:
		return "UA"
	case c&0xEF == 0x43:
		return "DM"
	case c&0xEF == 0x0F:
		return "DISC"
	case c&0xEF == 0xE3:
		return "TEST"
	default:
		return "?"
	}
}

// DefaultXID3 is a best-effort Type 2.1 XID3 information field, tuned
// empirically against SNA Server (override with -xid3). Layout (to iterate):
//
//	32                format 3 (hi nibble 3), node type 2 (T2.1)
//	00                reserved / characteristics
//	05 D0 00 02       node ID: IDBLK 05D, IDNUM 00002
//	00 00 00 00       fixed characteristics (placeholder — will need tuning)
var DefaultXID3 = []byte{
	0x32, 0x00, 0x05, 0xD0, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00,
}
