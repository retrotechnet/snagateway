// Command snagateway is an SNA-to-TN3270 gateway: it lets MS-DOS clients on a
// Microsoft SNA Server 4.0 network reach TN3270 hosts (Hercules, Sim390, etc.)
// that don't speak SNA.
//
// Subcommands:
//
//	tn3270  - connect to a TN3270 host and dump its screen (back-end test;
//	          works today, no SNA required)
//	run     - run the full gateway from a config file
//	version - print version
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"snagateway/internal/d3270"
	"snagateway/internal/tn3270"
)

var version = "0.1.0-dev"

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "tn3270":
		cmdTN3270(os.Args[2:])
	case "run":
		cmdRun(os.Args[2:])
	case "llc2-probe":
		cmdLLC2Probe(os.Args[2:])
	case "sna-probe":
		cmdSNAProbe(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("snagateway", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `snagateway - SNA (MS SNA Server) to TN3270 gateway

usage:
  snagateway tn3270     -addr HOST:PORT [-model IBM-3278-2] [-tn3270e] [-follow]
  snagateway llc2-probe -iface ens33 [-connect MAC] [-sap 0x04]
  snagateway sna-probe  -iface ens33 -connect MAC [-lus 2,3,4]
  snagateway sna-probe  -iface ens33 -connect MAC -show-file welcome.txt
  snagateway sna-probe  -iface ens33 -connect MAC -lus 2,3,4 -menu examples/menu/app.json
  snagateway run        -config config.json
  snagateway version

`)
}

func cmdTN3270(args []string) {
	fs := flag.NewFlagSet("tn3270", flag.ExitOnError)
	addr := fs.String("addr", "", "TN3270 host:port (e.g. 192.168.1.50:3270)")
	model := fs.String("model", "IBM-3278-2", "terminal model")
	tn3270e := fs.Bool("tn3270e", false, "attempt TN3270E (RFC 2355) negotiation")
	follow := fs.Bool("follow", false, "keep reading and re-rendering subsequent screens")
	verbose := fs.Bool("v", false, "log telnet/protocol negotiation")
	fs.Parse(args)

	if *addr == "" {
		fmt.Fprintln(os.Stderr, "tn3270: -addr is required")
		fs.Usage()
		os.Exit(2)
	}

	var plog *log.Logger
	if *verbose {
		plog = log.New(os.Stderr, "", log.Ltime)
	}

	c, err := tn3270.Dial(tn3270.Options{
		Addr:     *addr,
		TermType: *model,
		TN3270E:  *tn3270e,
		Timeout:  15 * time.Second,
		Logger:   plog,
	})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer c.Close()

	screen := d3270.NewScreen(*model)
	for {
		rec, err := c.ReadRecord()
		if err != nil {
			log.Fatalf("read: %v", err)
		}
		if len(rec) == 0 {
			continue
		}
		if err := screen.Apply(rec); err != nil {
			log.Printf("apply: %v (%d bytes, cmd 0x%02X)", err, len(rec), rec[0])
			continue
		}
		fmt.Print("\n" + screen.Render())
		if !*follow {
			// One screen is enough to confirm connectivity.
			fmt.Println("(connected OK; pass -follow to keep reading)")
			return
		}
	}
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", "config.json", "path to config file")
	fs.Parse(args)
	if err := runGateway(*cfgPath); err != nil {
		log.Fatalf("run: %v", err)
	}
}
