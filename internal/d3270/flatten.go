package d3270

// Flatten rewrites an inbound 3270 write data stream into base form for a
// terminal without extended-attribute support (e.g. a dependent LU running over
// the SSCP-LU session, which never negotiates color/extended attributes). It:
//
//   - reduces Start Field Extended (SFE) to a basic Start Field (SF) using the
//     field's basic attribute, dropping the color/highlight/extended pairs;
//   - drops Set Attribute (SA) orders;
//   - passes commands, WCC, SBA/RA/EUA, IC/PT, GE and character data through.
//
// Without this, a base-mode terminal renders the extended-attribute bytes as
// stray characters (the "?A&" / "?CO" garbage seen on the applet).
func Flatten(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	switch data[0] {
	case CmdEW, CmdEWA, CmdW:
		// a write data stream — flatten it below
	default:
		return data // WSF / read / unknown: leave untouched
	}

	out := make([]byte, 0, len(data))
	out = append(out, data[0]) // command
	i := 1
	if i < len(data) {
		out = append(out, data[i]) // WCC
		i++
	}

	for i < len(data) {
		b := data[i]
		switch b {
		case OrderSBA, OrderEUA:
			out = append(out, data[i:minInt(i+3, len(data))]...)
			i += 3
		case OrderRA:
			out = append(out, data[i:minInt(i+4, len(data))]...)
			i += 4
		case OrderSF:
			if i+1 < len(data) {
				out = append(out, OrderSF, data[i+1])
			}
			i += 2
		case OrderSFE:
			if i+1 >= len(data) {
				i++
				continue
			}
			npairs := int(data[i+1])
			j := i + 2
			var basic byte // default attribute if no basic (0xC0) pair present
			for p := 0; p < npairs && j+1 < len(data); p++ {
				if data[j] == 0xC0 { // basic field attribute pair
					basic = data[j+1]
				}
				j += 2
			}
			out = append(out, OrderSF, basic)
			i = j
		case OrderSA:
			i += 3 // drop Set Attribute (type + value)
		case OrderMF:
			if i+1 < len(data) {
				i += 2 + int(data[i+1])*2
			} else {
				i++
			}
		case OrderGE:
			out = append(out, data[i:minInt(i+2, len(data))]...)
			i += 2
		case OrderIC, OrderPT:
			out = append(out, b)
			i++
		default:
			out = append(out, b)
			i++
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
