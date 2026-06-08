package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"snagateway/internal/bridge"
	"snagateway/internal/config"
	"snagateway/internal/llc2"
	"snagateway/internal/sna"
	"snagateway/internal/tn3270"
)

// runGateway loads config, brings up the LLC2 front end, and for each LLC2 link
// runs an SNA session manager that bridges active LU2 sessions to TN3270.
func runGateway(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	logger := log.New(os.Stderr, "", log.Ltime)

	printSummary(cfg)

	localSAP, _ := cfg.LLC2.SAP()
	listener, err := llc2.Listen(llc2.Config{
		Interface: cfg.LLC2.Interface,
		LocalSAP:  localSAP,
	})
	if err != nil {
		if errors.Is(err, llc2.ErrNotImplemented) {
			logger.Printf("LLC2 front end not yet available: %v", err)
			logger.Printf("phases 1-2 (config + TN3270 back end) are ready; "+
				"try: snagateway tn3270 -addr %s", cfg.DefaultTarget.Addr())
			logger.Printf("phases 3-5 (LLC2 + SNA SSCP/PU5 + bridge wiring) remain — see README roadmap")
			return nil
		}
		return fmt.Errorf("llc2 listen: %w", err)
	}
	defer listener.Close()

	logger.Printf("listening for SNA Server on %s SAP 0x%02X", cfg.LLC2.Interface, localSAP)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go serveConn(cfg, conn, logger)
	}
}

// serveConn runs the SNA session manager for one accepted LLC2 link.
func serveConn(cfg *config.Config, conn llc2.Conn, logger *log.Logger) {
	defer conn.Close()
	connName := matchConnection(cfg, conn)
	logger.Printf("SNA Server link up: %s (mac %s)", connName, conn.RemoteMAC())

	lus := luAddresses(cfg, connName)
	mgr := sna.NewManager(conn, lus, func(s sna.LUSession) {
		// A session reached "data traffic active": dial the back end and bridge.
		target := cfg.TargetForLU(connName, int(s.LU()))
		host, err := tn3270.Dial(tn3270.Options{
			Addr:     target.Addr(),
			TermType: target.Model,
			TN3270E:  target.TN3270E,
			Logger:   logger,
		})
		if err != nil {
			logger.Printf("LU %d: back-end dial %s failed: %v", s.LU(), target.Addr(), err)
			s.Close()
			return
		}
		logger.Printf("LU %d bridged to %s (%s)", s.LU(), target.Addr(), target.Model)
		b := bridge.New(s, host, logger)
		if err := b.Run(); err != nil {
			logger.Printf("LU %d bridge ended: %v", s.LU(), err)
		}
		host.Close()
		s.Close()
	})

	if err := mgr.Run(); err != nil {
		logger.Printf("session manager for %s ended: %v", connName, err)
	}
}

func printSummary(cfg *config.Config) {
	fmt.Printf("snagateway %s\n", version)
	fmt.Printf("  LLC2: interface %s, local SAP %s\n", cfg.LLC2.Interface, cfg.LLC2.LocalSAP)
	fmt.Printf("  default back end: %s (%s)\n", cfg.DefaultTarget.Addr(), cfg.DefaultTarget.Model)
	for _, c := range cfg.Connections {
		fmt.Printf("  connection %q -> SNA Server %s SAP %s, %d LU(s)\n",
			c.Name, c.RemoteMAC, c.RemoteSAP, len(c.LUs))
		for _, l := range c.LUs {
			t := cfg.TargetForLU(c.Name, l.LU)
			fmt.Printf("    LU %d -> %s (%s)\n", l.LU, t.Addr(), t.Model)
		}
	}
	fmt.Println()
}

// matchConnection finds the configured connection name for a remote MAC.
func matchConnection(cfg *config.Config, conn llc2.Conn) string {
	remote := conn.RemoteMAC().String()
	for _, c := range cfg.Connections {
		if hw, err := c.HardwareAddr(); err == nil && hw.String() == remote {
			return c.Name
		}
	}
	return "(unknown:" + remote + ")"
}

// luAddresses returns the LU local addresses configured for a connection.
func luAddresses(cfg *config.Config, connName string) []byte {
	for _, c := range cfg.Connections {
		if c.Name != connName {
			continue
		}
		out := make([]byte, 0, len(c.LUs))
		for _, l := range c.LUs {
			out = append(out, byte(l.LU))
		}
		return out
	}
	return nil
}
