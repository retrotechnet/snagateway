package tn3270

// Telnet protocol constants (RFC 854) plus the options used by TN3270
// (RFC 1576) and TN3270E (RFC 2355).
const (
	iacIAC  = 255
	iacDONT = 254
	iacDO   = 253
	iacWONT = 252
	iacWILL = 251
	iacSB   = 250
	iacSE   = 240
	iacEOR  = 239 // End Of Record
)

// Telnet options.
const (
	optBINARY  = 0  // RFC 856
	optEOR     = 25 // RFC 885 End-of-record
	optTTYPE   = 24 // RFC 1091 Terminal-Type
	optTN3270E = 40 // RFC 2355
)

// TERMINAL-TYPE subnegotiation commands.
const (
	ttypeIS   = 0
	ttypeSEND = 1
)

// TN3270E subnegotiation message types (RFC 2355).
const (
	tn3270eASSOCIATE     = 0
	tn3270eCONNECT       = 1
	tn3270eDEVICE_TYPE   = 2
	tn3270eFUNCTIONS     = 3
	tn3270eIS            = 4
	tn3270eREASON        = 5
	tn3270eREJECT        = 6
	tn3270eREQUEST       = 7
	tn3270eSEND          = 8
)

// TN3270E data-message header DATA-TYPE values (first byte of the 5-byte
// header that prefixes each record in TN3270E mode).
const (
	dt3270Data    = 0x00
	dtSCSData     = 0x01
	dtResponse    = 0x02
	dtBindImage   = 0x03
	dtUnbind      = 0x04
	dtNVTData     = 0x05
	dtRequest     = 0x06
	dtSSCPLUData  = 0x07
	dtPrintEOJ    = 0x08
)
