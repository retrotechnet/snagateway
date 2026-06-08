package d3270

// EBCDIC <-> ASCII translation for code page 037 (US/Canada), sufficient for
// English-language 3270 screens. Unmapped EBCDIC bytes render as '.'.

var ebcdicToASCII [256]byte
var asciiToEBCDIC [256]byte

func init() {
	for i := range ebcdicToASCII {
		ebcdicToASCII[i] = '.'
	}
	set := func(e byte, a byte) {
		ebcdicToASCII[e] = a
		asciiToEBCDIC[a] = e
	}
	setRange := func(eStart byte, aStart, aEnd byte) {
		for a := aStart; a <= aEnd; a++ {
			set(eStart+(a-aStart), a)
		}
	}

	set(0x40, ' ')
	set(0x4B, '.')
	set(0x4C, '<')
	set(0x4D, '(')
	set(0x4E, '+')
	set(0x4F, '|')
	set(0x50, '&')
	set(0x5A, '!')
	set(0x5B, '$')
	set(0x5C, '*')
	set(0x5D, ')')
	set(0x5E, ';')
	set(0x60, '-')
	set(0x61, '/')
	set(0x6B, ',')
	set(0x6C, '%')
	set(0x6D, '_')
	set(0x6E, '>')
	set(0x6F, '?')
	set(0x7A, ':')
	set(0x7B, '#')
	set(0x7C, '@')
	set(0x7D, '\'')
	set(0x7E, '=')
	set(0x7F, '"')

	setRange(0x81, 'a', 'i') // 0x81-0x89
	setRange(0x91, 'j', 'r') // 0x91-0x99
	setRange(0xA2, 's', 'z') // 0xA2-0xA9

	set(0xC0, '{')
	setRange(0xC1, 'A', 'I') // 0xC1-0xC9
	set(0xD0, '}')
	setRange(0xD1, 'J', 'R') // 0xD1-0xD9
	set(0xE0, '\\')
	setRange(0xE2, 'S', 'Z') // 0xE2-0xE9
	setRange(0xF0, '0', '9') // 0xF0-0xF9

	// Make space the EBCDIC fallback for the ASCII->EBCDIC direction.
	for i := range asciiToEBCDIC {
		if asciiToEBCDIC[i] == 0 {
			asciiToEBCDIC[i] = 0x40
		}
	}
}

// E2A converts one EBCDIC byte to ASCII.
func E2A(b byte) byte { return ebcdicToASCII[b] }

// A2E converts one ASCII byte to EBCDIC.
func A2E(b byte) byte { return asciiToEBCDIC[b] }

// E2AString converts an EBCDIC byte slice to an ASCII string.
func E2AString(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = ebcdicToASCII[c]
	}
	return string(out)
}

// A2EBytes converts an ASCII string to EBCDIC bytes.
func A2EBytes(s string) []byte {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		out[i] = asciiToEBCDIC[s[i]]
	}
	return out
}
