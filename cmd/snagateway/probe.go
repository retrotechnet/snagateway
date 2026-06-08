package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"snagateway/internal/llc2"
)

// cmdLLC2Probe brings up an LLC2 link to SNA Server and logs link-up + inbound
// PIUs. Two modes:
//
//	passive (default): bind SAP and wait for SNA Server to call (listen/accept).
//	                   The kernel's listen path does NOT answer SNA's XID polls,
//	                   so this is expected to sit waiting.
//	active (-connect): dial SNA Server (which must be set to "Incoming"/called).
//	                   The kernel's active-open drives SABME and may complete the
//	                   link without the XID dance.
func cmdLLC2Probe(args []string) {
	fs := flag.NewFlagSet("llc2-probe", flag.ExitOnError)
	iface := fs.String("iface", "", "network interface, e.g. ens33")
	sapStr := fs.String("sap", "0x04", "local SAP (hex)")
	connect := fs.String("connect", "", "ACTIVE mode: dial this SNA Server MAC instead of listening")
	rsapStr := fs.String("rsap", "0x04", "remote SAP for -connect (hex)")
	fs.Parse(args)

	if *iface == "" {
		fmt.Fprintln(os.Stderr, "llc2-probe: -iface is required (e.g. -iface ens33)")
		fs.Usage()
		os.Exit(2)
	}
	sap, err := parseHexByte(*sapStr)
	if err != nil {
		log.Fatalf("llc2-probe: bad -sap %q: %v", *sapStr, err)
	}
	cfg := llc2.Config{Interface: *iface, LocalSAP: sap}

	if *connect != "" {
		mac, err := net.ParseMAC(*connect)
		if err != nil {
			log.Fatalf("llc2-probe: bad -connect MAC %q: %v", *connect, err)
		}
		rsap, err := parseHexByte(*rsapStr)
		if err != nil {
			log.Fatalf("llc2-probe: bad -rsap %q: %v", *rsapStr, err)
		}
		log.Printf("llc2-probe: ACTIVE dial -> %s SAP 0x%02X (from %s SAP 0x%02X); set SNA Server connection to Incoming",
			mac, rsap, *iface, sap)
		conn, err := llc2.Dial(cfg, mac, rsap)
		if err != nil {
			log.Fatalf("llc2-probe: dial: %v", err)
		}
		log.Printf("llc2-probe: LINK UP (active) -> %s SAP 0x%02X", conn.RemoteMAC(), conn.RemoteSAP())
		readPIUs(conn)
		return
	}

	l, err := llc2.Listen(cfg)
	if err != nil {
		log.Fatalf("llc2-probe: listen: %v", err)
	}
	defer l.Close()
	log.Printf("llc2-probe: PASSIVE listen on %s SAP 0x%02X — waiting for SNA Server to bring the link up", *iface, sap)
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatalf("llc2-probe: accept: %v", err)
		}
		log.Printf("llc2-probe: LINK UP <- %s SAP 0x%02X", conn.RemoteMAC(), conn.RemoteSAP())
		go readPIUs(conn)
	}
}

// readPIUs logs every inbound PIU until the link drops.
func readPIUs(c llc2.Conn) {
	defer c.Close()
	for {
		piu, err := c.Read()
		if err != nil {
			log.Printf("llc2-probe: read from %s ended: %v", c.RemoteMAC(), err)
			return
		}
		log.Printf("llc2-probe: inbound PIU %d bytes: % X", len(piu), piu)
	}
}

func parseHexByte(s string) (byte, error) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	v, err := strconv.ParseUint(s, 16, 8)
	if err != nil {
		return 0, err
	}
	return byte(v), nil
}
