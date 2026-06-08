// Package d3270 holds the 3270 data-stream definitions shared by the TN3270
// back end and (eventually) the SNA LU2 front end: commands, orders, AIDs,
// EBCDIC translation, buffer-address coding, and a minimal screen model used
// to render incoming data streams for testing/logging.
//
// The 3270 data stream is identical whether it arrives over TN3270 (telnet
// framed) or SNA LU2 (RU framed), which is what makes the gateway a mostly
// mechanical bridge.
package d3270

// 3270 commands (the first byte of a write data stream).
const (
	CmdW    = 0xF1 // Write
	CmdEW   = 0xF5 // Erase/Write
	CmdEWA  = 0x7E // Erase/Write Alternate
	CmdRB   = 0xF2 // Read Buffer
	CmdRM   = 0xF6 // Read Modified
	CmdRMA  = 0x6E // Read Modified All
	CmdEAU  = 0x6F // Erase All Unprotected
	CmdWSF  = 0xF3 // Write Structured Field
	CmdNOP  = 0x03 // No-op
)

// 3270 orders (embedded in a write data stream).
const (
	OrderSF  = 0x1D // Start Field
	OrderSFE = 0x29 // Start Field Extended
	OrderSBA = 0x11 // Set Buffer Address
	OrderSA  = 0x28 // Set Attribute
	OrderMF  = 0x2C // Modify Field
	OrderIC  = 0x13 // Insert Cursor
	OrderPT  = 0x05 // Program Tab
	OrderRA  = 0x3C // Repeat to Address
	OrderEUA = 0x12 // Erase Unprotected to Address
	OrderGE  = 0x08 // Graphic Escape
)

// AID (Attention IDentifier) codes sent by the terminal in a read response.
const (
	AIDNone    = 0x60
	AIDEnter   = 0x7D
	AIDPF1     = 0xF1
	AIDPF2     = 0xF2
	AIDPF3     = 0xF3
	AIDClear   = 0x6D
	AIDPA1     = 0x6C
	AIDPA2     = 0x6E
	AIDPA3     = 0x6B
	AIDSysReq  = 0xF0
	AIDStructF = 0x88 // structured field (TN3270E / query reply)
)

// Field attribute bits (3270 basic attribute byte).
const (
	AttrProtected   = 0x20
	AttrNumeric     = 0x10
	AttrDisplayMask = 0x0C
	AttrIntensified = 0x08 // within display mask
	AttrNonDisplay  = 0x0C // within display mask
	AttrModified    = 0x01 // MDT
)

// Geometry returns the (rows, cols) for a 3270 model string. Defaults to
// model 2 (24x80) for anything unrecognized.
func Geometry(model string) (rows, cols int) {
	switch normModel(model) {
	case "2":
		return 24, 80
	case "3":
		return 32, 80
	case "4":
		return 43, 80
	case "5":
		return 27, 132
	default:
		return 24, 80
	}
}

// normModel extracts the trailing model digit from strings like
// "IBM-3278-2", "IBM-3279-2-E", "3278-2".
func normModel(m string) string {
	digits := ""
	for i := 0; i < len(m); i++ {
		c := m[i]
		if c >= '0' && c <= '9' {
			digits += string(c)
		} else if c == '-' {
			digits = "" // reset; keep only the last numeric group between dashes
		}
	}
	if len(digits) == 1 {
		return digits
	}
	return "2"
}

// DecodeAddr decodes a 12-bit 3270 buffer address (the common model-2 case).
func DecodeAddr(b0, b1 byte) int {
	return (int(b0&0x3F) << 6) | int(b1&0x3F)
}

// sbaCode is the 6-bit address-byte translation table used when encoding
// 12-bit buffer addresses, so each emitted byte avoids control values.
var sbaCode = [64]byte{
	0x40, 0xC1, 0xC2, 0xC3, 0xC4, 0xC5, 0xC6, 0xC7,
	0xC8, 0xC9, 0x4A, 0x4B, 0x4C, 0x4D, 0x4E, 0x4F,
	0x50, 0xD1, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7,
	0xD8, 0xD9, 0x5A, 0x5B, 0x5C, 0x5D, 0x5E, 0x5F,
	0x60, 0x61, 0xE2, 0xE3, 0xE4, 0xE5, 0xE6, 0xE7,
	0xE8, 0xE9, 0x6A, 0x6B, 0x6C, 0x6D, 0x6E, 0x6F,
	0xF0, 0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7,
	0xF8, 0xF9, 0x7A, 0x7B, 0x7C, 0x7D, 0x7E, 0x7F,
}

// EncodeAddr encodes a buffer address as two 12-bit address bytes.
func EncodeAddr(addr int) (byte, byte) {
	return sbaCode[(addr>>6)&0x3F], sbaCode[addr&0x3F]
}
