package sna

import "fmt"

// DecodeSense renders a 4-byte SNA sense code as a human-readable string. The
// most important signal is the category (the high byte): 0x08 (request reject)
// and 0x10 (request error) mean the request was DELIVERED to the half-session
// and rejected on its content — e.g. a BIND with bad parameters, which we can
// fix. 0x80 (path error) means the PIU could not be routed/delivered at all —
// no session exists for that address — which points at an address-assignment
// (LFSID / XID3) problem rather than the BIND image. Distinguishing those two
// is the whole question for the LU-LU BIND, so decode precisely.
func DecodeSense(sense uint32) string {
	if sense == 0 {
		return "0x00000000 (no sense)"
	}
	cat := byte(sense >> 24)
	specific := uint16(sense >> 16) // category + modifier, the part that names the error

	if name, ok := senseNames[specific]; ok {
		return fmt.Sprintf("0x%08X %s: %s", sense, categoryName(cat), name)
	}
	return fmt.Sprintf("0x%08X %s (unrecognized modifier 0x%02X)", sense, categoryName(cat), byte(specific))
}

// categoryName names the sense-code category (high byte). This alone answers
// "did SNA Server reject the BIND's content, or fail to route it?".
func categoryName(cat byte) string {
	switch cat {
	case 0x00:
		return "user-sense"
	case 0x08:
		return "request-reject"
	case 0x10:
		return "request-error"
	case 0x20:
		return "state-error"
	case 0x40:
		return "RH-usage-error"
	case 0x80:
		return "PATH-ERROR"
	default:
		return fmt.Sprintf("category-0x%02X", cat)
	}
}

// senseNames maps the 2-byte category+modifier to the standard meaning. Keyed by
// the high 16 bits of the 4-byte sense (the low 16 bits are code-specific data).
var senseNames = map[uint16]string{
	// 0x08xx — Request Reject: the request reached the LU/half-session and was
	// rejected. For a BIND, these mean the BIND itself is malformed/unacceptable.
	0x0801: "resource not available",
	0x0805: "session limit exceeded",
	0x0806: "resource unknown",
	0x0809: "mode inconsistency (request not allowed in this session state)",
	0x080A: "permission rejected (partner won't allow the session/bracket)",
	0x080C: "procedure not supported",
	0x080E: "component not available",
	0x080F: "invalid session parameters (BIND image not acceptable)",
	0x0812: "insufficient resource",
	0x0815: "bracket bid reject — no RTR",
	0x081C: "request not executable",
	0x0820: "bracket bid reject — RTR forthcoming",
	0x0821: "invalid session parameters (BIND image field invalid)",
	0x0822: "class of service / virtual route not available",
	0x0826: "ERP message forthcoming",
	0x0831: "queued response error",
	0x0835: "invalid parameter (a fixed field in the request is invalid)",
	0x0845: "SSCP-LU session not active / LU not enabled for the request",
	0x084B: "requested resources not available",
	0x084C: "permanent insufficient resource",

	// 0x10xx — Request Error: the receiver could not interpret the RU.
	0x1001: "RU data error",
	0x1002: "RU length error",
	0x1003: "function not supported",
	0x1005: "parameter error (a value in the RU is out of range)",
	0x1007: "category not supported",
	0x100B: "function abort",

	// 0x20xx — State Error: protocol-state / sequencing violation.
	0x2001: "sequence number error",
	0x2002: "chaining error",
	0x2003: "bracket state error",
	0x2004: "direction error (data sent against change-direction)",
	0x2005: "data traffic reset (no SDT) — RU not allowed before SDT",
	0x2008: "no begin bracket",
	0x200A: "immediate-request-mode error",
	0x200E: "response correlation error",

	// 0x40xx — RH Usage Error.
	0x4003: "bracket indicators not allowed",
	0x4004: "incorrect use of format indicator (FI)",
	0x4007: "exception response not allowed",
	0x400B: "chaining not supported",

	// 0x80xx — Path Error: the PIU could not be routed/delivered. For a BIND
	// this means SNA Server has no address binding for our LFSID — i.e. the
	// address-assignment (LFSID / XID3) convention, NOT the BIND image.
	0x8001: "intermediate node / link failure (no route)",
	0x8002: "invalid FID or TH format",
	0x8005: "no session — no active half-session for this address (LFSID)",
	0x800A: "node failure",
	0x800F: "no half-session / address not assigned for this LFSID",
}
