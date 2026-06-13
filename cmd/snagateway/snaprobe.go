package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"snagateway/internal/bridge"
	"snagateway/internal/d3270"
	"snagateway/internal/llc2"
	"snagateway/internal/sna"
	"snagateway/internal/tn3270"
)

// cmdSNAProbe dials SNA Server (active LLC2 open), then drives the host-side SNA
// activation sequence: ACTPU, then ACTLU for each LU, logging and parsing every
// response. After activation it keeps reading so we can observe what SNA Server
// sends next (NOTIFY, logon/INIT-SELF, etc.). RU formats are overridable via
// hex flags so we can tune them against live responses without recompiling.
func cmdSNAProbe(args []string) {
	fs := flag.NewFlagSet("sna-probe", flag.ExitOnError)
	iface := fs.String("iface", "", "network interface, e.g. ens33")
	connect := fs.String("connect", "", "SNA Server MAC to dial (active open)")
	sapStr := fs.String("sap", "0x04", "local & remote SAP (hex)")
	lusStr := fs.String("lus", "2", "comma-separated LU local addresses to ACTLU")
	actpuHex := fs.String("actpu", "", "override ACTPU RU (hex, e.g. 110105...)")
	actluHex := fs.String("actlu", "", "override ACTLU RU (hex, e.g. 0D0101)")
	waitSec := fs.Int("wait", 5, "seconds to wait for each response")
	dialTries := fs.Int("dialtries", 15, "dial attempts (each ~3s) to catch SNA Server's ~19s call cycle")
	bind := fs.Bool("bind", true, "auto-send BIND+SDT when an LU signals a session request (NOTIFY status 03)")
	bindHex := fs.String("bind-image", "", "override BIND image RU (hex)")
	plu := fs.Int("plu", 0, "PLU (host) local address used as the LU-LU session OAF")
	odai := fs.Bool("odai", false, "ODAI bit for the LU-LU session (true = peripheral-assigned LFSID)")
	bindSweep := fs.Bool("bind-sweep", false, "on logon, sweep LU-LU LFSIDs (OAF/ODAI) looking for one SNA Server accepts")
	target := fs.String("target", "", "TN3270 back end host:port to bridge a bound LU session to (e.g. 10.0.0.14:3270)")
	targetModel := fs.String("target-model", "IBM-3278-2", "TN3270 terminal model for -target")
	ussTest := fs.Bool("uss-test", false, "send a test 3270 screen over the SSCP-LU session (no BIND) to check the display path")
	showFile := fs.String("show-file", "", "on applet attach, display this text file over the SSCP-LU session (no BIND); repaints on each connect")
	echoTest := fs.Bool("echo-test", false, "diagnostic: prompt over the SSCP-LU session and echo typed input back, to verify the interactive input loop works after our display")
	clearHex := fs.String("clear", "0C", "echo-test: hex bytes prepended to each screen to clear/home the applet (0C=FF, 15=NL, empty=none); the rest is clean character-coded text")
	fmtTest := fs.Bool("fmt", false, "echo-test: send a real 3270 datastream (EW+SBA) with the RH format-indicator set, to test if the applet processes 3270 orders on the SSCP-LU session")
	pageTest := fs.Bool("page", false, "echo-test: send a full 24-line page (NL-separated) so the previous page scrolls out of the applet's 24-line window — an effective full-screen clear")
	dump := fs.Bool("dump", false, "hex-dump the SSCP-LU 3270 data streams (original from host + flattened) for debugging")
	fs.Parse(args)

	if *dump {
		sna.OnSSCPLUSend = func(original, flattened []byte) {
			n := func(b []byte) []byte {
				if len(b) > 192 {
					return b[:192]
				}
				return b
			}
			log.Printf("sna-probe: SSCP-LU host orig %d bytes: % X", len(original), n(original))
			log.Printf("sna-probe: SSCP-LU flattened %d bytes: % X", len(flattened), n(flattened))
		}
	}

	bindImage := sna.DefaultBind
	if *bindHex != "" {
		bindImage = mustHex(*bindHex)
	}
	clearBytes := mustHex(*clearHex) // echo-test screen-clear prefix (e.g. FF)

	if *iface == "" || *connect == "" {
		fmt.Fprintln(os.Stderr, "sna-probe: -iface and -connect are required")
		fs.Usage()
		os.Exit(2)
	}
	sap, err := parseHexByte(*sapStr)
	if err != nil {
		log.Fatalf("sna-probe: bad -sap %q: %v", *sapStr, err)
	}
	mac, err := net.ParseMAC(*connect)
	if err != nil {
		log.Fatalf("sna-probe: bad -connect %q: %v", *connect, err)
	}
	lus, err := parseLUs(*lusStr)
	if err != nil {
		log.Fatalf("sna-probe: bad -lus %q: %v", *lusStr, err)
	}
	actpuRU := sna.DefaultActPU
	if *actpuHex != "" {
		actpuRU = mustHex(*actpuHex)
	}
	actluRU := sna.DefaultActLU
	if *actluHex != "" {
		actluRU = mustHex(*actluHex)
	}

	log.Printf("sna-probe: dialing %s SAP 0x%02X (up to %d tries) ...", mac, sap, *dialTries)
	cfg := llc2.Config{Interface: *iface, LocalSAP: sap}
	var conn llc2.Conn
	for attempt := 1; ; attempt++ {
		conn, err = llc2.Dial(cfg, mac, sap)
		if err == nil {
			break
		}
		if attempt >= *dialTries {
			log.Fatalf("sna-probe: dial failed after %d attempts: %v\n"+
				"  - is the CRESSIDA connection Active and set to Outgoing Calls?\n"+
				"  - if it's stuck in [Pending], its link station is stale: stop/start the SNA Service to clear it.\n"+
				"  (this build sends a clean DISC on Ctrl-C/SIGTERM, which prevents that wedge going forward)", attempt, err)
		}
		log.Printf("sna-probe: dial attempt %d/%d: %v — retrying", attempt, *dialTries, err)
		time.Sleep(1 * time.Second)
	}
	defer conn.Close()
	log.Printf("sna-probe: LINK UP -> %s", conn.RemoteMAC())

	// Clean shutdown: on Ctrl-C / SIGTERM, close the LLC2 link so the kernel
	// sends DISC and SNA Server tears its link station down. Without this, an
	// abrupt exit leaves SNA Server's connection wedged in [Pending] and the
	// next dial is rejected (software caused connection abort) until the SNA
	// Service is restarted. A short grace period lets the DISC reach the wire.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("sna-probe: signal received — disconnecting LLC2 link cleanly (DISC)")
		_ = conn.Close()
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()

	pr := &piuReader{conn: conn} // splits coalesced PIUs out of each LLC2 read
	wait := time.Duration(*waitSec) * time.Second
	var snf uint16 = 0x0001

	send(conn, "ACTPU", sna.BuildActPU(snf, actpuRU))
	if _, ok := recv(pr, wait); !ok {
		log.Printf("sna-probe: no ACTPU response — aborting (the PU never activated)")
		return
	}

	for _, lu := range lus {
		snf++
		send(conn, fmt.Sprintf("ACTLU(LU %d)", lu), sna.BuildActLU(lu, snf, actluRU))
		recv(pr, wait)
	}

	log.Printf("sna-probe: activation sequence done; observing + auto-acking inbound requests (Ctrl-C to stop)")
	_ = conn.SetReadDeadline(time.Time{}) // block indefinitely
	bound := false
	var session *sna.LU2Session // non-nil once the LU-LU session is bridged
	// One persistent SSCP-LU session per LU for -show-file, keyed by LU local
	// address, so each client's banner is addressed to its own LU (correct DAF)
	// and keeps a monotonic SNF across that LU's reconnects.
	fileSessions := map[byte]*sna.LU2Session{}
	for {
		piu, err := pr.Read()
		if err != nil {
			log.Printf("sna-probe: read ended: %v", err)
			return
		}
		summary, _ := sna.DescribeResponse(piu)
		log.Printf("sna-probe: <- %-3d bytes: % X  [%s]", len(piu), piu, summary)

		p, perr := sna.ParsePIU(piu)
		if perr != nil || p.RH.Response {
			continue
		}

		// Auto-acknowledge inbound requests (NOTIFY, logon, terminal input) so
		// the dialog and bracket protocol keep flowing.
		if rsp, rerr := sna.BuildPositiveResponse(piu); rerr == nil {
			log.Printf("sna-probe: -> +RSP(%s)  % X", sna.RequestName(p), rsp)
			if werr := conn.Write(rsp); werr != nil {
				log.Printf("sna-probe:    +RSP write failed: %v", werr)
			}
		}

		// Client disconnected (NOTIFY status 01 while a bridge is active): tear
		// the bridge down and reset so a reconnect starts a fresh session
		// (otherwise the gateway needs a restart to reconnect the applet).
		if session != nil && isAppIdleNotify(p) {
			log.Printf("sna-probe: LU %d client disconnected — closing bridge", p.TH.OAF)
			session.Close()
			session = nil
			continue
		}

		// Echo-test mode: the de-risking spike for the interactive menu. On applet
		// attach, prompt for a line; on each line typed, decode the EBCDIC input and
		// echo it back. If the typed text round-trips on the applet (not just an
		// empty Enter), the SSCP-LU session supports the display+input loop a menu
		// needs. Reuses the per-LU SSCP-LU session cache.
		if *echoTest {
			lu := p.TH.OAF
			// Send clean character-coded text (linearized, no 3270 command/orders —
			// those render as garbage here), prefixed with clearBytes (e.g. FF) to
			// test what clears/homes the applet between messages.
			if isAppReadyNotify(p) {
				snf++
				echoSend(conn, lu, snf, *pageTest, *fmtTest, clearBytes, *targetModel, "SNAGATEWAY  --  ECHO TEST", "", "TYPE A LINE, THEN PRESS ENTER:")
				log.Printf("sna-probe: echo-test: prompted LU %d (page=%v fmt=%v clear=% X)", lu, *pageTest, *fmtTest, clearBytes)
				continue
			}
			if isLogonRequest(p) { // char-coded input typed at the applet
				text := strings.TrimRight(d3270.E2AString(p.RU), " \x00")
				log.Printf("sna-probe: echo-test: LU %d typed %d bytes: raw=% X decoded=%q", lu, len(p.RU), p.RU, text)
				snf++
				echoSend(conn, lu, snf, *pageTest, *fmtTest, clearBytes, *targetModel, "YOU TYPED:", "    ["+text+"]", "", "TYPE ANOTHER LINE, THEN PRESS ENTER:")
				continue
			}
			continue
		}

		// Show-file mode: on applet attach (NOTIFY status 03), paint a static
		// text file over the SSCP-LU session and nothing else (no BIND, no
		// bridge). The file is re-read and repainted on every attach, so editing
		// it on the gateway takes effect on the next connect, and reconnects
		// always redraw. One persistent session keeps the SNF monotonic.
		if *showFile != "" && isAppReadyNotify(p) {
			lu := p.TH.OAF
			sess := fileSessions[lu]
			if sess == nil {
				sess = sna.NewSSCPLUSession(conn, lu, *targetModel)
				fileSessions[lu] = sess
			}
			ds, err := showFileDatastream(*showFile)
			if err != nil {
				log.Printf("sna-probe: show-file %q: %v", *showFile, err)
				continue
			}
			if err := sess.SendToTerminal(ds); err != nil {
				log.Printf("sna-probe: show-file send failed (LU %d): %v", lu, err)
			} else {
				log.Printf("sna-probe: displayed %s on LU %d", *showFile, lu)
			}
			continue
		}

		// USS mode: when the applet signals it's ready (NOTIFY status 03), either
		// bridge a TN3270 host over the SSCP-LU session (-target) or paint a
		// static test screen — both without a BIND.
		if *ussTest && session == nil && isAppReadyNotify(p) {
			lu := p.TH.OAF
			if *target != "" {
				session = startSSCPLUBridge(conn, lu, *target, *targetModel)
			} else {
				screen := uss3270TestScreen()
				snf++
				log.Printf("sna-probe: -> USS test screen on SSCP-LU (LU %d): %d bytes", lu, len(screen))
				if werr := conn.Write(sna.BuildSSCPLUData(lu, snf, screen)); werr != nil {
					log.Printf("sna-probe:    USS screen write failed: %v", werr)
				}
			}
			continue
		}

		// Once bridged, inbound FMD data is the terminal's input (AID + modified
		// fields) — hand it to the bridge to forward to the TN3270 host.
		if session != nil && p.RH.Category == sna.CategoryFMD && !p.RH.FI {
			session.Deliver(p.RU)
			continue
		}

		// Host-initiated session: the LU's logon is an FMD request (FI=0) on the
		// SSCP-LU session, after the NOTIFY status-03. On it, drive BIND then SDT
		// to bring up the LU-LU session, then bridge it to the TN3270 back end.
		if *bind && !*ussTest && *showFile == "" && !*echoTest && !bound && isLogonRequest(p) {
			bound = true
			lu := p.TH.OAF
			if *bindSweep {
				sweepBind(conn, pr, lu, bindImage, &snf)
				_ = conn.SetReadDeadline(time.Time{})
				continue
			}
			snf++
			send(conn, fmt.Sprintf("BIND(LU %d)", lu), sna.BuildBind(lu, byte(*plu), *odai, snf, bindImage))
			rsp, ok := recv(pr, wait)
			if !ok {
				bound = false
				_ = conn.SetReadDeadline(time.Time{})
				continue
			}
			if _, positive := sna.DescribeResponse(rsp); !positive {
				log.Printf("sna-probe:    BIND rejected — not sending SDT")
				bound = false
				_ = conn.SetReadDeadline(time.Time{})
				continue
			}
			snf++
			send(conn, fmt.Sprintf("SDT(LU %d)", lu), sna.BuildSDT(lu, byte(*plu), *odai, snf))
			recv(pr, wait)
			_ = conn.SetReadDeadline(time.Time{}) // back to blocking observe

			// Session is up: bridge it to the TN3270 back end if configured.
			if *target != "" {
				session = startBridge(conn, lu, byte(*plu), *odai, *target, *targetModel)
			} else {
				log.Printf("sna-probe: LU %d session active (no -target; not bridging)", lu)
			}
		}
	}
}

func send(c llc2.Conn, name string, piu []byte) {
	log.Printf("sna-probe: -> %-12s % X", name, piu)
	if err := c.Write(piu); err != nil {
		log.Printf("sna-probe: write %s failed: %v", name, err)
	}
}

// piuReader wraps an llc2.Conn and hands out one SNA PIU per call, splitting any
// coalesced PIUs the kernel returned in a single read (see sna.SplitPIUs).
type piuReader struct {
	conn  llc2.Conn
	queue [][]byte
}

func (r *piuReader) dequeue() []byte {
	piu := r.queue[0]
	r.queue = r.queue[1:]
	return piu
}

// Read returns the next PIU, blocking until one is available.
func (r *piuReader) Read() ([]byte, error) {
	for len(r.queue) == 0 {
		_ = r.conn.SetReadDeadline(time.Time{})
		buf, err := r.conn.Read()
		if err != nil {
			return nil, err
		}
		r.queue = sna.SplitPIUs(buf)
	}
	return r.dequeue(), nil
}

// ReadTimeout returns the next PIU, waiting at most wait for a fresh read.
func (r *piuReader) ReadTimeout(wait time.Duration) ([]byte, error) {
	if len(r.queue) > 0 {
		return r.dequeue(), nil
	}
	_ = r.conn.SetReadDeadline(time.Now().Add(wait))
	buf, err := r.conn.Read()
	if err != nil {
		return nil, err
	}
	r.queue = sna.SplitPIUs(buf)
	return r.dequeue(), nil
}

func recv(r *piuReader, wait time.Duration) ([]byte, bool) {
	piu, err := r.ReadTimeout(wait)
	if err != nil {
		log.Printf("sna-probe: <- (no response within %s: %v)", wait, err)
		return nil, false
	}
	summary, _ := sna.DescribeResponse(piu)
	log.Printf("sna-probe: <- %-3d bytes: % X  [%s]", len(piu), piu, summary)
	return piu, true
}

// startBridge dials the TN3270 back end and couples it to a freshly-bound LU2
// session, pumping 3270 data both ways. Returns the session (so the read loop
// can Deliver inbound terminal input), or nil if the back end couldn't be dialed.
func startBridge(conn llc2.Conn, lu, plu byte, odai bool, target, model string) *sna.LU2Session {
	host, err := tn3270.Dial(tn3270.Options{Addr: target, TermType: model, Timeout: 15 * time.Second})
	if err != nil {
		log.Printf("sna-probe: LU %d: back-end dial %s failed: %v (session up but not bridged)", lu, target, err)
		return nil
	}
	session := sna.NewLUSession(conn, lu, plu, odai)
	log.Printf("sna-probe: LU %d bridged to %s (%s)", lu, target, model)
	go func() {
		if err := bridge.New(session, host, log.Default()).Run(); err != nil {
			log.Printf("sna-probe: LU %d bridge ended: %v", lu, err)
		}
		host.Close()
		session.Close()
	}()
	return session
}

// sweepBind tries a range of LU-LU LFSIDs (OAF + ODAI) for the BIND, logging the
// response class for each, to discover the one SNA Server will accept. ODAI=1
// (peripheral-assigned) is tried first since those attempts were processed
// (sense 800F) rather than dropped. Stops on a positive response.
func sweepBind(conn llc2.Conn, pr *piuReader, lu byte, image []byte, snf *uint16) {
	const wait = 2 * time.Second
	log.Printf("sna-probe: BIND sweep starting (LU %d) ...", lu)
	for _, odai := range []bool{true, false} {
		for oaf := byte(1); oaf <= 12; oaf++ {
			if oaf == lu {
				continue
			}
			*snf++
			_ = conn.Write(sna.BuildBind(lu, oaf, odai, *snf, image))
			rsp, ok := recv(pr, wait)
			if !ok {
				log.Printf("sna-probe:   OAF=%2d ODAI=%-5v -> (silent)", oaf, odai)
				continue
			}
			summary, positive := sna.DescribeResponse(rsp)
			log.Printf("sna-probe:   OAF=%2d ODAI=%-5v -> %s", oaf, odai, summary)
			if positive {
				log.Printf("sna-probe: *** BIND ACCEPTED: OAF=%d ODAI=%v *** (report this!)", oaf, odai)
				return
			}
		}
	}
	log.Printf("sna-probe: BIND sweep exhausted — no LFSID accepted")
}

// startSSCPLUBridge dials the TN3270 back end and relays its 3270 screens to the
// terminal over the SSCP-LU session (USS mode — no BIND). Inbound terminal data
// on the SSCP-LU session is forwarded to the host. Returns the session, or nil
// if the back end couldn't be dialed.
func startSSCPLUBridge(conn llc2.Conn, lu byte, target, model string) *sna.LU2Session {
	host, err := tn3270.Dial(tn3270.Options{Addr: target, TermType: model, Timeout: 15 * time.Second})
	if err != nil {
		log.Printf("sna-probe: LU %d: back-end dial %s failed: %v", lu, target, err)
		return nil
	}
	session := sna.NewSSCPLUSession(conn, lu, model)
	log.Printf("sna-probe: LU %d bridged to %s over the SSCP-LU session (%s)", lu, target, model)
	go func() {
		if err := bridge.New(session, host, log.Default()).Run(); err != nil {
			log.Printf("sna-probe: LU %d SSCP-LU bridge ended: %v", lu, err)
		}
		host.Close()
		session.Close()
	}()
	return session
}

// showFileDatastream reads a text file and builds a 3270 Erase/Write data stream
// that lays it out at model-2 geometry (24 rows x 80 cols): each text line is
// placed at the start of its row via SBA and translated to EBCDIC. Lines longer
// than 80 columns are truncated; rows past 24 are dropped (with a warning). The
// stream is sent over the SSCP-LU session, which renders it into the screen
// buffer and re-emits pure character-coded text — so it displays cleanly on the
// applet (which would otherwise show raw orders as garbage).
func showFileDatastream(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	const rows, cols = 24, 80
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > rows {
		log.Printf("sna-probe: show-file: %d lines, showing first %d (screen is %dx%d)", len(lines), rows, rows, cols)
		lines = lines[:rows]
	}
	out := []byte{d3270.CmdEW, 0xC3} // Erase/Write + WCC (reset MDT, unlock keyboard)
	for row, line := range lines {
		line = sanitizeLine(line, cols)
		if line == "" {
			continue // no order/text needed for a blank row (screen is erased)
		}
		b0, b1 := d3270.EncodeAddr(row * cols)
		out = append(out, d3270.OrderSBA, b0, b1)
		out = append(out, d3270.A2EBytes(line)...)
	}
	return out, nil
}

// echoSend writes one echo-test screen. page mode sends a full 24-line page that
// scrolls the previous page out of the applet's window (effective clear). fmt mode
// sends a real 3270 datastream with the RH format-indicator set (tests 3270-order
// processing). Otherwise it sends clean character-coded text prefixed with the
// clear-probe bytes.
func echoSend(conn llc2.Conn, lu byte, snf uint16, page, fmt bool, clear []byte, model string, lines ...string) {
	var piu []byte
	switch {
	case page:
		piu = sna.BuildSSCPLUData(lu, snf, pageScreen(lines...))
	case fmt:
		th := sna.TH{MPF: sna.MPFWhole, DAF: lu, OAF: 0x00, SNF: snf}
		rh := sna.RH{Category: sna.CategoryFMD, FI: true, BCI: true, ECI: true, DR1: true}
		piu = sna.BuildPIU(th, rh, echoScreen(lines...))
	default:
		piu = sna.BuildSSCPLUData(lu, snf, charScreen(clear, model, lines...))
	}
	_ = conn.Write(piu)
}

// pageScreen builds a full 24-line character-coded page: a leading NL (0x15) ends
// any partial line from prior input, then 24 lines (padded with blanks) separated
// by NL, each capped at 79 columns so the applet's 80-column auto-wrap doesn't add
// extra lines. Because the applet only shows the last 24 lines, a full 24-line
// page pushes all previous content out of view — a clean full-screen clear using
// only the NL behavior the applet honors.
func pageScreen(lines ...string) []byte {
	const rows = 24
	out := []byte{0x15} // leading NL: terminate any partial line (e.g. the user's input)
	for i := 0; i < rows; i++ {
		if i > 0 {
			out = append(out, 0x15)
		}
		var line string
		if i < len(lines) {
			line = lines[i]
		}
		if len(line) > 79 {
			line = line[:79]
		}
		out = append(out, d3270.A2EBytes(line)...)
	}
	return out
}

// charScreen builds clean character-coded SSCP-LU display data: it lays the lines
// out in a 3270 screen buffer (which applies the positioning), then linearizes to
// pure EBCDIC text with no command or order bytes (those render as garbage on the
// applet's SSCP-LU session). clear is prepended unchanged — a candidate clear/home
// control (e.g. FF=0x0C) we're probing for.
func charScreen(clear []byte, model string, lines ...string) []byte {
	scr := d3270.NewScreen(model)
	_ = scr.Apply(echoScreen(lines...))
	return append(append([]byte{}, clear...), scr.Linear()...)
}

// echoScreen builds a 3270 Erase/Write data stream that places each given line at
// the start of its own row (model-2 geometry), for the echo-test diagnostic. Sent
// over the SSCP-LU session, which renders it to clean character-coded text.
func echoScreen(lines ...string) []byte {
	out := []byte{d3270.CmdEW, 0xC3} // Erase/Write + WCC (reset MDT, unlock keyboard)
	for row, line := range lines {
		if line == "" {
			continue
		}
		b0, b1 := d3270.EncodeAddr(row * 80)
		out = append(out, d3270.OrderSBA, b0, b1)
		out = append(out, d3270.A2EBytes(line)...)
	}
	return out
}

// sanitizeLine expands tabs to spaces, replaces non-printable ASCII with spaces
// (so a stray byte can't be taken for a 3270 order), and truncates to width.
func sanitizeLine(s string, width int) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7E {
			b = append(b, ' ')
		} else {
			b = append(b, c)
		}
	}
	if len(b) > width {
		b = b[:width]
	}
	return string(b)
}

// isAppReadyNotify reports whether a request PIU is a NOTIFY (NS 81 06 20) with
// status byte 0x03 — the LU signaling that a client/applet has attached.
func isAppReadyNotify(p *sna.PIU) bool {
	return isNotify(p) && p.RU[5] == 0x03
}

// isAppIdleNotify reports whether a request PIU is a NOTIFY with status byte
// 0x01 — the LU signaling it is idle (e.g. the client disconnected).
func isAppIdleNotify(p *sna.PIU) bool {
	return isNotify(p) && p.RU[5] == 0x01
}

func isNotify(p *sna.PIU) bool {
	return len(p.RU) >= 6 && p.RU[0] == 0x81 && p.RU[1] == 0x06 && p.RU[2] == 0x20
}

// uss3270TestScreen builds a simple 3270 Erase/Write data stream to display over
// the SSCP-LU session, to verify the screen reaches the terminal without a BIND.
func uss3270TestScreen() []byte {
	out := []byte{d3270.CmdEW, 0xC3} // Erase/Write + WCC (reset MDT, restore keyboard)
	sba := func(row, col int) {
		b0, b1 := d3270.EncodeAddr(row*80 + col)
		out = append(out, d3270.OrderSBA, b0, b1)
	}
	sba(2, 20)
	out = append(out, d3270.A2EBytes("SNAGATEWAY  --  SSCP-LU DISPLAY TEST")...)
	sba(4, 20)
	out = append(out, d3270.A2EBytes("If you can read this on the applet,")...)
	sba(5, 20)
	out = append(out, d3270.A2EBytes("the SSCP-LU 3270 data path works.")...)
	return out
}

// isLogonRequest reports whether a request PIU is the dependent LU's USS logon:
// an FMD request WITHOUT a format indicator (FI=0 = raw character-coded data, no
// NS header). NOTIFY is also FMD but has FI=1, so this excludes it. Empty RU = a
// bare Enter; non-empty = a typed command (e.g. "test" = A3 85 A2 A3). This is
// our cue to drive the BIND.
func isLogonRequest(p *sna.PIU) bool {
	return p.RH.Category == sna.CategoryFMD && !p.RH.FI
}

func parseLUs(s string) ([]byte, error) {
	var out []byte
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return nil, err
		}
		if v < 0 || v > 255 {
			return nil, fmt.Errorf("LU %d out of range 0-255", v)
		}
		out = append(out, byte(v))
	}
	return out, nil
}

func mustHex(s string) []byte {
	s = strings.ReplaceAll(strings.ReplaceAll(s, " ", ""), "0x", "")
	b, err := hex.DecodeString(s)
	if err != nil {
		log.Fatalf("sna-probe: bad hex %q: %v", s, err)
	}
	return b
}
