package sna

import (
	"encoding/binary"
	"fmt"
)

// Default activation RUs, from observed SNA traces (IBM SNA Formats; SDLC
// walkthroughs). Overridable at the call site so formats can be tuned against a
// live host that rejects them.
var (
	// ACTPU cold: 11=ACTPU, 01=cold, 01=FM profile 0 / TS profile 1 (SSCP-PU),
	// +SSCP ID. Byte 2 = 0x01 confirmed against MS SNA Server 4.0 SP2 (0x05 was
	// rejected with sense 0835 "invalid parameter" at that offset).
	DefaultActPU = []byte{0x11, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}
	// ACTLU cold: 0D=ACTLU, 01=cold, 01=FM profile 0 / TS profile 1 (SSCP-LU).
	DefaultActLU = []byte{0x0D, 0x01, 0x01}

	// DefaultBind is a 3270 LU2 (display, model 2 / 24x80) BIND image, assembled
	// from the IBM 3278-2 logmode SNX32702:
	//   31          BIND
	//   01          format 0, negotiable
	//   03 03       FMPROF 3 / TSPROF 3 (LU-LU 3270)
	//   B1 90       PRIPROT / SECPROT
	//   30 80       COMPROT
	//   00 00       TS usage / sec pacing window (no pacing)
	//   87 87       RUSIZES (pri->sec / sec->pri)
	//   00 00       primary/secondary send pacing windows (none)
	//   02 00 00 00 00 00 18 50 18 50 7F 00   PSERVIC: LU2, rows=24(0x18) cols=80(0x50)
	// 26 bytes total. Negotiable, so the SLU may return a corrected image in its
	// +RSP. Tune via -bind-image if rejected.
	DefaultBind = []byte{
		0x31, 0x01, 0x03, 0x03, 0xB1, 0x90, 0x30, 0x80,
		0x00, 0x00, 0x87, 0x87, 0x00, 0x00,
		0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x18, 0x50, 0x18, 0x50, 0x7F, 0x00,
	}
)

// scActivationHeaders builds the TH/RH common to ACTPU/ACTLU: FID2 whole BIU on
// the expedited flow (EFI=1, byte0=0x2D), Session Control request requesting a
// definite response (RH 6B 80 00).
func scActivationHeaders(daf, oaf byte, snf uint16) (TH, RH) {
	return TH{MPF: MPFWhole, EFI: true, DAF: daf, OAF: oaf, SNF: snf},
		RH{Category: CategorySC, FI: true, BCI: true, ECI: true, DR1: true}
}

// BuildActPU builds an ACTPU PIU (SSCP -> PU, DAF=0).
func BuildActPU(snf uint16, ru []byte) []byte {
	th, rh := scActivationHeaders(0x00, 0x00, snf)
	return BuildPIU(th, rh, ru)
}

// BuildActLU builds an ACTLU PIU (SSCP -> LU at local address lu).
func BuildActLU(lu byte, snf uint16, ru []byte) []byte {
	th, rh := scActivationHeaders(lu, 0x00, snf)
	return BuildPIU(th, rh, ru)
}

// BuildBind builds a BIND PIU (PLU/host -> SLU at local address lu) carrying the
// given BIND image, establishing the LU-LU (3270) session. BIND (and UNBIND)
// flow on the NORMAL flow (EFI=0, TH byte0=0x2C) — unlike ACTPU/ACTLU/SDT, which
// are expedited (EFI=1). Sending BIND expedited draws sense 0809 "mode
// inconsistency".
func BuildBind(lu, plu byte, odai bool, snf uint16, image []byte) []byte {
	// LU-LU session LFSID = {OAF=plu, DAF=lu, ODAI}. It must differ from the
	// SSCP-LU session's {0, lu, ODAI=0}. BIND is normal flow (EFI=0).
	th := TH{MPF: MPFWhole, ODAI: odai, EFI: false, DAF: lu, OAF: plu, SNF: snf}
	rh := RH{Category: CategorySC, FI: true, BCI: true, ECI: true, DR1: true}
	return BuildPIU(th, rh, image)
}

// BuildSDT builds a Start Data Traffic PIU (host -> SLU), sent after +RSP(BIND)
// to allow normal-flow data on the LU-LU session. SDT is expedited (EFI=1) and
// uses the same LU-LU LFSID as BIND.
func BuildSDT(lu, plu byte, odai bool, snf uint16) []byte {
	th := TH{MPF: MPFWhole, ODAI: odai, EFI: true, DAF: lu, OAF: plu, SNF: snf}
	rh := RH{Category: CategorySC, FI: true, BCI: true, ECI: true, DR1: true}
	return BuildPIU(th, rh, []byte{RUSDT})
}

// DescribeResponse parses a response PIU and returns a human-readable summary
// plus whether it was a positive response.
func DescribeResponse(raw []byte) (summary string, positive bool) {
	p, err := ParsePIU(raw)
	if err != nil {
		return fmt.Sprintf("unparseable PIU: %v", err), false
	}
	if !p.RH.Response {
		return fmt.Sprintf("REQUEST %s DAF=%d OAF=%d RU=% X",
			RequestName(p), p.TH.DAF, p.TH.OAF, p.RU), false
	}
	if p.RH.RTI || p.RH.ERI { // negative response
		// Negative RSP RU layout: 4-byte sense code, then the request code.
		sense := uint32(0)
		code := byte(0)
		if len(p.RU) >= 4 {
			sense = binary.BigEndian.Uint32(p.RU[:4])
		}
		if len(p.RU) >= 5 {
			code = p.RU[4]
		}
		return fmt.Sprintf("NEGATIVE RSP to 0x%02X (DAF=%d OAF=%d) sense=%s",
			code, p.TH.DAF, p.TH.OAF, DecodeSense(sense)), false
	}
	return fmt.Sprintf("POSITIVE RSP to 0x%02X (DAF=%d OAF=%d)",
		p.RequestCode(), p.TH.DAF, p.TH.OAF), true
}

// RequestName gives a short label for a request PIU: session-control commands
// by their 1-byte code, network-services requests by their 3-byte NS code.
func RequestName(p *PIU) string {
	if p.RH.Category == CategorySC && len(p.RU) >= 1 {
		switch p.RU[0] {
		case RUActPU:
			return "ACTPU"
		case RUActLU:
			return "ACTLU"
		case RUBind:
			return "BIND"
		case RUUnbind:
			return "UNBIND"
		case RUSDT:
			return "SDT"
		}
	}
	if len(p.RU) >= 3 && p.RU[0] == 0x81 && p.RU[1] == 0x06 {
		switch p.RU[2] {
		case 0x20:
			return "NOTIFY"
		case 0x81:
			return "INIT-SELF"
		case 0x21:
			return "REQDISCONT"
		}
	}
	return fmt.Sprintf("code=0x%02X", p.RequestCode())
}

// BuildPositiveResponse constructs a +RSP to an inbound request PIU: addresses
// swapped, response bit set, the request's definite-response flag preserved, and
// the request code echoed (1 byte for session control, 3 bytes for an NS RU).
func BuildPositiveResponse(request []byte) ([]byte, error) {
	p, err := ParsePIU(request)
	if err != nil {
		return nil, err
	}
	if p.RH.Response {
		return nil, fmt.Errorf("sna: not a request (can't +RSP a response)")
	}
	th := TH{MPF: MPFWhole, ODAI: p.TH.ODAI, EFI: p.TH.EFI, DAF: p.TH.OAF, OAF: p.TH.DAF, SNF: p.TH.SNF}
	rh := RH{Response: true, Category: p.RH.Category, FI: p.RH.FI, BCI: true, ECI: true, DR1: p.RH.DR1, DR2: p.RH.DR2}

	// +RSP RU: echo the request code for session-control (1 byte) and
	// network-services (3-byte NS code) requests; plain FMD/DFC responses carry
	// no RU.
	var ru []byte
	switch {
	case p.RH.Category == CategorySC && len(p.RU) >= 1:
		ru = []byte{p.RU[0]}
	case p.RH.FI && len(p.RU) >= 3:
		ru = append(ru, p.RU[:3]...)
	}
	return BuildPIU(th, rh, ru), nil
}
