package d3270

// Flatten rewrites an inbound 3270 write data stream into UNFORMATTED form for a
// terminal that processes only Erase/Write, WCC, SBA and character data — which
// is how the MS SNA Server 3270 applet behaves over the SSCP-LU session (it does
// not interpret field orders, printing them as garbage). It:
//
//   - replaces each Start Field (SF) and Start Field Extended (SFE) with a single
//     blank cell — preserving the one screen position a field attribute occupies,
//     which a real terminal shows blank anyway;
//   - drops Set Attribute (SA) orders;
//   - passes commands, WCC, SBA/RA/EUA, IC/PT, GE and character data through.
//
// The result is positioned text with no field structure, which the base-mode
// terminal renders cleanly (monochrome — color/fields need a full LU-LU session).
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
			out = append(out, 0x40) // field attribute cell -> blank
			i += 2
		case OrderSFE:
			if i+1 >= len(data) {
				i++
				continue
			}
			out = append(out, 0x40) // field attribute cell -> blank
			i += 2 + int(data[i+1])*2
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
