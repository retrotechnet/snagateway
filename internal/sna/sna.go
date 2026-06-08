// Package sna implements the minimal host-side SNA needed to present dependent
// LU2 (3270 display) sessions to Microsoft SNA Server: a small SSCP/PU5 that
// activates SNA Server's PU and LUs, drives BIND/SDT, and carries the 3270 data
// stream in FMD request units.
//
// This package owns the SNA framing (Transmission Header / Request-Response
// Header, both implemented below) and the session state machine (scaffolded in
// session.go). It sits above internal/llc2 (which delivers whole PIUs) and
// feeds 3270 data streams to internal/bridge.
package sna

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ---- FID2 Transmission Header (6 bytes) -------------------------------------
//
//	byte0: FID(4) | MPF(2) | reserved(1) | EFI(1)
//	byte1: reserved
//	byte2: DAF  (destination address field, the local LU address)
//	byte3: OAF  (origin address field)
//	byte4-5: SNF (sequence number field)

// Mapping field (MPF) values.
const (
	MPFMiddle = 0x0 // middle segment
	MPFLast   = 0x1 // last segment
	MPFFirst  = 0x2 // first segment
	MPFWhole  = 0x3 // whole (only) segment
)

// TH is a parsed FID2 transmission header.
type TH struct {
	MPF  byte // mapping field
	ODAI bool // OAF-DAF assignor indicator; part of the session identifier
	EFI  bool // expedited flow indicator
	DAF  byte // destination address (LU local address)
	OAF  byte // origin address
	SNF  uint16
}

// ParseTH parses a 6-byte FID2 transmission header.
func ParseTH(b []byte) (TH, error) {
	if len(b) < 6 {
		return TH{}, fmt.Errorf("sna: TH too short (%d bytes)", len(b))
	}
	if fid := b[0] >> 4; fid != 2 {
		return TH{}, fmt.Errorf("sna: unsupported FID %d (only FID2)", fid)
	}
	return TH{
		MPF:  (b[0] >> 2) & 0x3,
		ODAI: b[0]&0x02 != 0,
		EFI:  b[0]&0x01 != 0,
		DAF:  b[2],
		OAF:  b[3],
		SNF:  binary.BigEndian.Uint16(b[4:6]),
	}, nil
}

// Bytes serializes a FID2 transmission header.
func (t TH) Bytes() []byte {
	b := make([]byte, 6)
	b[0] = 2<<4 | (t.MPF&0x3)<<2
	if t.ODAI {
		b[0] |= 0x02
	}
	if t.EFI {
		b[0] |= 0x01
	}
	b[2] = t.DAF
	b[3] = t.OAF
	binary.BigEndian.PutUint16(b[4:6], t.SNF)
	return b
}

// ---- Request/Response Header (3 bytes) --------------------------------------

// RU categories (RH byte0 bits 1-2).
const (
	CategoryFMD = 0x0 // function management data (e.g. 3270 data stream)
	CategoryNC  = 0x1 // network control
	CategoryDFC = 0x2 // data flow control
	CategorySC  = 0x3 // session control (ACTLU/BIND/SDT/...)
)

// RH is a parsed request/response header.
type RH struct {
	Response bool // RRI: true=response, false=request
	Category byte // CategoryFMD/NC/DFC/SC
	FI       bool // format indicator
	SDI      bool // sense-data included
	BCI      bool // begin chain
	ECI      bool // end chain
	DR1      bool // definite response 1 requested/returned
	DR2      bool // definite response 2 requested/returned
	ERI      bool // exception response indicator
	RTI      bool // response type indicator (response only: true=negative)
	BBI      bool // begin bracket
	EBI      bool // end bracket
	CDI      bool // change direction
	CEBI     bool // conditional end bracket
}

// ParseRH parses a 3-byte request/response header.
func ParseRH(b []byte) (RH, error) {
	if len(b) < 3 {
		return RH{}, fmt.Errorf("sna: RH too short (%d bytes)", len(b))
	}
	return RH{
		Response: b[0]&0x80 != 0,
		Category: (b[0] >> 5) & 0x03,
		FI:       b[0]&0x08 != 0,
		SDI:      b[0]&0x04 != 0,
		BCI:      b[0]&0x02 != 0,
		ECI:      b[0]&0x01 != 0,
		DR1:      b[1]&0x80 != 0,
		DR2:      b[1]&0x20 != 0,
		ERI:      b[1]&0x10 != 0,
		RTI:      b[1]&0x04 != 0,
		BBI:      b[2]&0x80 != 0,
		EBI:      b[2]&0x40 != 0,
		CDI:      b[2]&0x20 != 0,
		CEBI:     b[2]&0x10 != 0,
	}, nil
}

// Bytes serializes a request/response header.
func (h RH) Bytes() []byte {
	b := make([]byte, 3)
	if h.Response {
		b[0] |= 0x80
	}
	b[0] |= (h.Category & 0x03) << 5
	if h.FI {
		b[0] |= 0x08
	}
	if h.SDI {
		b[0] |= 0x04
	}
	if h.BCI {
		b[0] |= 0x02
	}
	if h.ECI {
		b[0] |= 0x01
	}
	if h.DR1 {
		b[1] |= 0x80
	}
	if h.DR2 {
		b[1] |= 0x20
	}
	if h.ERI {
		b[1] |= 0x10
	}
	if h.RTI {
		b[1] |= 0x04
	}
	if h.BBI {
		b[2] |= 0x80
	}
	if h.EBI {
		b[2] |= 0x40
	}
	if h.CDI {
		b[2] |= 0x20
	}
	if h.CEBI {
		b[2] |= 0x10
	}
	return b
}

// ---- Session Control RU request codes (first RU byte, SC category) ----------
const (
	RUActLU   = 0x0D
	RUDactLU  = 0x0E
	RUActPU   = 0x11
	RUDactPU  = 0x12
	RUBind    = 0x31
	RUUnbind  = 0x32
	RUSDT     = 0xA0 // Start Data Traffic
	RUClear   = 0xA1
	RUSTSN    = 0xA2 // Set and Test Sequence Numbers
	RURQR     = 0xA3 // Request Recovery
)

// ---- A few common SNA sense codes (4 bytes, big-endian) ---------------------
const (
	SenseNone          uint32 = 0x00000000
	SenseInvalidFormat uint32 = 0x10010000
	SenseLUBusy        uint32 = 0x08010000
	SenseSessionLimit  uint32 = 0x08050000
	SenseModeInconsist uint32 = 0x08210000
	SenseFunctionUnsup uint32 = 0x10030000
)

// PIU is a fully-parsed path information unit (one LLC2 I-frame payload).
type PIU struct {
	TH  TH
	RH  RH
	RU  []byte // request/response unit (may be empty)
	Raw []byte
}

// ErrShortPIU indicates a PIU smaller than TH+RH.
var ErrShortPIU = errors.New("sna: PIU shorter than TH+RH (9 bytes)")

// ParsePIU splits a raw PIU into TH, RH, and RU.
func ParsePIU(raw []byte) (*PIU, error) {
	if len(raw) < 9 {
		return nil, ErrShortPIU
	}
	th, err := ParseTH(raw[:6])
	if err != nil {
		return nil, err
	}
	rh, err := ParseRH(raw[6:9])
	if err != nil {
		return nil, err
	}
	return &PIU{TH: th, RH: rh, RU: raw[9:], Raw: raw}, nil
}

// Build assembles a PIU from a TH, RH, and RU.
func BuildPIU(th TH, rh RH, ru []byte) []byte {
	out := make([]byte, 0, 9+len(ru))
	out = append(out, th.Bytes()...)
	out = append(out, rh.Bytes()...)
	out = append(out, ru...)
	return out
}

// RequestCode returns the first RU byte (the request code) or 0 if empty.
func (p *PIU) RequestCode() byte {
	if len(p.RU) == 0 {
		return 0
	}
	return p.RU[0]
}

// SplitPIUs splits a buffer of one or more coalesced FID2 PIUs into individual
// PIUs. The Linux AF_LLC SOCK_STREAM socket coalesces LLC2 I-frames, and FID2
// has no length field, so a single Read can return several PIUs back-to-back.
//
// This is a best-effort splitter: a new PIU begins at any FID2 TH (byte0 high
// nibble = 2, byte1 reserved = 0) at least 9 bytes (TH+RH) into the current one.
// Reliable for SNA control flows, whose RUs don't contain that 2-byte pattern; a
// 3270-data RU could in principle, so fully robust LU-LU data framing would need
// RU-length parsing or a message-preserving transport.
func SplitPIUs(buf []byte) [][]byte {
	if len(buf) == 0 {
		return nil
	}
	var out [][]byte
	start := 0
	for {
		end := len(buf)
		for j := start + 9; j+1 < len(buf); j++ {
			if buf[j]&0xF0 == 0x20 && buf[j+1] == 0x00 { // FID2 TH start
				end = j
				break
			}
		}
		out = append(out, buf[start:end])
		if end >= len(buf) {
			break
		}
		start = end
	}
	return out
}
