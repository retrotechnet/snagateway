// Package llc2 is the 802.2 / LLC type 2 link layer toward Microsoft SNA
// Server. SNA Server's "802.2" link service establishes a connection-oriented
// LLC2 link to the gateway's MAC address + SAP; over that link flow the SNA
// PIUs (Transmission Header + Request/Response Header + RU) that the sna
// package interprets.
//
// LLC2 is non-routable: the NT4 SNA Server box and this gateway must share a
// layer-2 Ethernet segment (same switch/VLAN, or bridged VM NICs).
//
// # Implementation status
//
// This is a scaffold. The interfaces below are stable; Listen returns
// ErrNotImplemented until the Linux AF_LLC binding is filled in. The recipe is
// documented on Listen so it can be implemented and tested directly against
// SNA Server (it cannot be meaningfully tested without it).
package llc2

import (
	"errors"
	"net"
	"time"
)

// ErrNotImplemented is returned by Listen until the AF_LLC binding lands.
var ErrNotImplemented = errors.New("llc2: AF_LLC binding not yet implemented (see Listen doc)")

// Config describes the local 802.2 endpoint.
type Config struct {
	Interface string // Linux NIC name, e.g. "eth0"
	LocalSAP  byte   // conventionally 0x04 for SNA
}

// Listener accepts inbound LLC2 connections from SNA Server.
type Listener interface {
	// Accept blocks until SNA Server establishes an LLC2 link, returning a
	// connection that carries SNA PIUs as its I-frame payloads.
	Accept() (Conn, error)
	Close() error
}

// Conn is one established LLC2 link to a remote PU (SNA Server). Read and Write
// transfer whole LLC2 information-field payloads — i.e. one SNA PIU each.
type Conn interface {
	// Read returns the next inbound I-frame payload (a complete SNA PIU:
	// TH+RH+RU).
	Read() ([]byte, error)
	// Write sends one I-frame payload (a complete SNA PIU).
	Write(piu []byte) error
	// RemoteMAC is the SNA Server NIC address on the far end.
	RemoteMAC() net.HardwareAddr
	// RemoteSAP is the source SAP used by SNA Server (typically 0x04).
	RemoteSAP() byte
	// SetReadDeadline bounds future Read calls; the zero time disables it.
	// A timed-out Read returns an error wrapping syscall.EAGAIN.
	SetReadDeadline(t time.Time) error
	Close() error
}

// Listen opens an AF_LLC SOCK_STREAM socket bound to cfg.Interface's MAC and
// cfg.LocalSAP, and listens for SNA Server to bring the link up.
//
// # AF_LLC recipe (Linux)
//
// The Linux kernel exposes connection-oriented LLC2 via AF_LLC (family 26).
// There is no stdlib Sockaddr for it, so the socket is driven with raw
// syscalls. Outline:
//
//	fd, _ := syscall.Socket(AF_LLC /*26*/, syscall.SOCK_STREAM, 0)
//
//	// struct sockaddr_llc {
//	//   __kernel_sa_family_t sllc_family;  // AF_LLC
//	//   __kernel_sa_family_t sllc_arphrd;  // ARPHRD_ETHER (1)
//	//   unsigned char sllc_test, sllc_xid, sllc_ua;
//	//   unsigned char sllc_sap;            // LocalSAP (0x04)
//	//   unsigned char sllc_mac[IFHWADDRLEN /*6*/];
//	//   unsigned char __pad[2];
//	// };
//	// Fill sllc_mac from the NIC's hardware address (net.InterfaceByName),
//	// sllc_sap = cfg.LocalSAP, then bind/listen:
//	syscall.Syscall(SYS_BIND,   fd, &sa, sizeof(sa))
//	syscall.Listen(fd, backlog)
//
//	// Accept yields a new fd plus the peer sockaddr_llc (remote MAC + SAP):
//	nfd, peer := syscall.Syscall(SYS_ACCEPT, fd, &peerSa, &peerLen)
//
// Each accepted fd is a reliable, sequenced LLC2 stream. Read()/Write() on the
// Conn map to syscall.Read/Write of one information field == one SNA PIU.
// (The kernel handles SABME/UA, XID, I-frame sequencing, RR/RNR flow control.)
//
// Prerequisites on the gateway: `modprobe llc2`, the NIC in promiscuous or at
// least able to receive frames for SAP 0x04, and SNA Server configured with an
// 802.2 connection whose remote address is this NIC's MAC.
func Listen(cfg Config) (Listener, error) {
	return listen(cfg)
}

// Dial actively opens an LLC2 link to a remote station (e.g. SNA Server
// configured as the "Incoming"/called side). The kernel's active-open path
// drives SABME itself, which can complete a link where the passive listen path
// won't answer SNA's XID polls. Returns once the link is up (UA received).
func Dial(cfg Config, remoteMAC net.HardwareAddr, remoteSAP byte) (Conn, error) {
	return dial(cfg, remoteMAC, remoteSAP)
}
