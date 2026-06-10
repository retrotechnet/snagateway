package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	"snagateway/internal/xid"
)

// cmdXIDProbe answers SNA Server's XID poll with a candidate XID3 over AF_PACKET
// and logs SNA Server's frames, to find out whether the XID3 is accepted (SNA
// Server then sends SABME) or rejected (it keeps polling XID or sends DM). The
// XID3 is tuned with -xid3 until SABME follows.
func cmdXIDProbe(args []string) {
	fs := flag.NewFlagSet("xid-probe", flag.ExitOnError)
	iface := fs.String("iface", "", "network interface, e.g. ens33")
	connect := fs.String("connect", "", "SNA Server MAC (only react to its frames)")
	sapStr := fs.String("sap", "0x04", "SAP (hex)")
	xid3Hex := fs.String("xid3", "", "override the XID3 information field (hex)")
	fs.Parse(args)

	if *iface == "" || *connect == "" {
		fmt.Fprintln(os.Stderr, "xid-probe: -iface and -connect are required")
		fs.Usage()
		os.Exit(2)
	}
	mac, err := net.ParseMAC(*connect)
	if err != nil {
		log.Fatalf("xid-probe: bad -connect %q: %v", *connect, err)
	}
	sap, err := parseHexByte(*sapStr)
	if err != nil {
		log.Fatalf("xid-probe: bad -sap %q: %v", *sapStr, err)
	}
	xid3 := xid.DefaultXID3
	if *xid3Hex != "" {
		xid3 = mustHex(*xid3Hex)
	}

	r, err := xid.NewResponder(*iface, mac, sap)
	if err != nil {
		log.Fatalf("xid-probe: %v", err)
	}
	defer r.Close()
	log.Printf("xid-probe: listening on %s SAP 0x%02X for XID from %s", *iface, sap, mac)
	log.Printf("xid-probe: will answer XID with XID3: % X", xid3)
	log.Printf("xid-probe: (set SNA Server connection to Outgoing so it polls with XID)")

	for {
		frame, err := r.Recv()
		if err != nil {
			log.Fatalf("xid-probe: recv: %v", err)
		}
		f, ok := xid.ParseLLCFrame(frame)
		if !ok || !bytes.Equal(f.Src, mac) {
			continue // not an 802.3 LLC frame from SNA Server
		}
		log.Printf("xid-probe: <- dsap=%02X ssap=%02X ctrl=%02X [%s] info=% X",
			f.DSAP, f.SSAP, f.Control, xid.ControlName(f.Control), f.Info)

		if xid.IsXID(f.Control) {
			if err := r.SendXID(xid3); err != nil {
				log.Printf("xid-probe:    XID3 send failed: %v", err)
			} else {
				log.Printf("xid-probe: -> XID3 (% d bytes) sent", len(xid3))
			}
		}
		if xid.IsSABME(f.Control) {
			log.Printf("xid-probe: *** SNA Server sent SABME — XID3 ACCEPTED ***")
		}
	}
}
