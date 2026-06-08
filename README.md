# snagateway

An SNA-to-TN3270 gateway for retro restoration. It lets **MS-DOS clients running the
Microsoft SNA Server client** reach **TN3270 hosts** (Hercules, Sim390, or any
TN3270/TCP system) even though those hosts don't speak SNA.

```
MS-DOS client (DOS SNA client + 3270 applet)
   ⇄  IPX/SPX or TCP over NetWare 3.12
NT 4.0  ⇄  Microsoft SNA Server 4.0 SP2
   ⇄  802.2 / LLC2 over Ethernet          ← "host connection" link service
Linux gateway  (THIS PROGRAM)
   ⇄  TN3270 over TCP/IP
Hercules / Sim390 / any TN3270 host
```

## The core idea

Microsoft SNA Server is **not** a host. It is a PU 2.0 peripheral node that pools
*dependent* LUs and expects a **host** (an SSCP-owning subarea node) to activate them.

So this gateway **impersonates the mainframe/FEP toward SNA Server** — it owns an SSCP,
sends `ACTPU`/`ACTLU`, drives `BIND`/`SDT`, and carries the 3270 data stream in SNA
request units — while simultaneously acting as a **TN3270 client toward the emulator**.

The 3270 *data stream* is essentially identical on both sides; TN3270 wraps it in telnet
framing (RFC 1576 / 2355) and SNA LU2 wraps it in RU chaining + responses. Most of the
gateway is therefore **envelope translation**, plus a from-scratch (but minimal)
dependent-LU2 host emulation, because no off-the-shelf host-side SNA stack exists for
Linux. (The kernel's `AF_LLC`/`CONFIG_LLC2` link layer is a leftover of an *abandoned*
1990s attempt to build exactly this.)

## Network boundaries (why the gateway never speaks IPX/SPX)

SNA Server has two **independent** boundaries, and the gateway only touches one of them:

```
MS-DOS clients ──IPX/SPX──► SNA Server ──802.2 / LLC2──► gateway ──TN3270/TCP──► host
              (client/server)          (host link service)
```

1. **Client ↔ SNA Server** — SNA Server's "client/server" protocol. SNA Server 4.0 can
   carry it over Named Pipes, TCP/IP, NetBEUI, Banyan VINES, or **IPX/SPX**. The DOS
   clients use IPX/SPX (the natural fit on NetWare 3.12). This conversation is strictly
   between the DOS client and the NT4 server; **the gateway is not a party to it and never
   sees IPX.** SNA Server terminates the IPX client session internally and re-presents that
   LU's traffic out over the 802.2 link as SNA PIUs.

2. **SNA Server ↔ gateway** — the host link service we implement: raw **IEEE 802.2 LLC
   type 2** frames on the Ethernet, addressed by MAC + SAP (0x04). No IP, no IPX. This is
   the *only* protocol the gateway speaks toward SNA Server (via the Linux `llc2` kernel
   module / `AF_LLC`).

So the gateway needs exactly one capability on the SNA side: send/receive 802.2 LLC2 frames
on the shared Ethernet segment. It needs **no IPX stack, no IP address on the SNA side, and
no NetWare awareness** — it can be IPX-silent. Notes for a single-wire setup:

- IPX/SPX (NetWare uses 802.3-raw or 802.2 framing) and our LLC2/SAP-0x04 traffic coexist
  on the same Ethernet — different SAPs, no collision.
- The gateway only needs **layer-2 adjacency with the SNA Server's NIC** (same switch/VLAN);
  it does not need to join the NetWare IPX network number.
- The gateway's TCP/IP side (to Hercules/Sim390) is a separate, unrelated path.

## Scope (current target)

- Transport to SNA Server: **802.2 / LLC2** over Ethernet (NT4 box and gateway must share
  a layer-2 segment — same switch/VLAN or bridged VM NICs; LLC2 is non-routable).
- LU types: **LU2 display only** (3278/3279 models).
- Back end: **TN3270** (basic RFC 1576; optional TN3270E / RFC 2355).

## Build order / status

| Phase | Package           | Status                | Notes                                                |
|-------|-------------------|-----------------------|------------------------------------------------------|
| 1     | `internal/config` |  working              | config load                                          |
| 2     | `internal/tn3270` |  **proven**           | renders a live Hercules screen                       |
| 2     | `internal/d3270`  |  **proven**           | 3270 data-stream parse/render                        |
| 3     | `internal/llc2`   |  **proven**           | LLC2 link up via kernel `AF_LLC` (active dial)       |
| 4a    | `internal/sna`    |  **proven**           | ACTPU/ACTLU/NOTIFY/USS-logon all work vs SNA Svr     |
| 4c    | `internal/sna`    |  **BIND blocked**     | host-initiated BIND rejected (sense 0809); WIP       |
| 5     | `internal/bridge` |  built, untested      | wired into `sna-probe -target`; runs once BIND lands |

**Status:** every layer works *except* the host-initiated **BIND** that activates the LU-LU
session (SNA Server returns sense `0809`). The TN3270 back end, the LLC2 link, PU/LU
activation, the SSCP-LU dialog, and the bridge are all in place — the moment BIND succeeds, a
logon on the DOS/3270 client auto-connects to the TN3270 host. See the proven end-to-end
back end:

```sh
./snagateway tn3270 -addr <hercules>:3270 -v   # renders the real mainframe screen
```

The full (BIND-gated) path is exercised by:

```sh
./snagateway sna-probe -iface ens33 -connect <sna-server-mac> -lus 2 -target <hercules>:3270
```

## Building

Authored on Windows but **targets Linux** (LLC2 / `AF_LLC` is Linux-only). Zero external
dependencies — standard library only.

```sh
# native (on the Linux gateway)
go build -o snagateway ./cmd/snagateway

# cross-compile from any OS
GOOS=linux GOARCH=amd64 go build -o snagateway ./cmd/snagateway
```

## Trying the TN3270 back-end now

This works before any SNA exists — point it at a Hercises/Sim390 device port and it will
negotiate, read the first screen, and dump it:

```sh
./snagateway tn3270 -addr 192.168.1.50:3270           # basic RFC 1576
./snagateway tn3270 -addr 192.168.1.50:3270 -tn3270e  # TN3270E / RFC 2355
```

## Running the full gateway

```sh
./snagateway run -config config.json
```

(LLC2/SNA front-end is scaffolded; see the phase table. `run` currently brings up config
+ back-end wiring and logs where the SNA layers still need implementing.)

## Layout

```
cmd/snagateway      entry point, subcommands
internal/config     config model (LU → TN3270 target mapping)
internal/tn3270     TN3270 client (telnet negotiation, record framing)
internal/d3270      3270 data-stream constants, EBCDIC, screen parse/render
internal/llc2       802.2 / LLC2 link layer (AF_LLC)        [scaffold]
internal/sna        SSCP/PU5 host emulation, RU/TH/RH types [scaffold]
internal/bridge     ties an SNA LU2 session ↔ a TN3270 conn [scaffold]
```
