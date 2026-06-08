//go:build linux

package llc2

import (
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Linux AF_LLC binding. The kernel's llc2 module provides a connection-oriented
// LLC type 2 service: once a socket is bound to a SAP, the kernel handles XID,
// TEST, SABME/UA and I-frame sequencing. A SOCK_STREAM listener accepts inbound
// links (SNA Server, configured as the caller, initiates) and each accepted fd
// carries SNA PIUs as its information-field payloads.

const (
	afLLC       = 26 // AF_LLC
	arphrdEther = 1  // ARPHRD_ETHER
)

// rawSockaddrLLC mirrors the kernel's struct sockaddr_llc (16 bytes).
type rawSockaddrLLC struct {
	Family uint16 // AF_LLC
	Arphrd uint16 // ARPHRD_ETHER
	Test   uint8
	Xid    uint8
	Ua     uint8
	Sap    uint8
	Mac    [6]uint8
	Pad    [2]uint8
}

type llcListener struct {
	fd  int
	cfg Config
}

type llcConn struct {
	fd        int
	remoteMAC net.HardwareAddr
	remoteSAP byte
	wmu       sync.Mutex // serializes Write so concurrent senders don't interleave PIUs
}

func listen(cfg Config) (Listener, error) {
	iface, err := net.InterfaceByName(cfg.Interface)
	if err != nil {
		return nil, fmt.Errorf("llc2: interface %q: %w", cfg.Interface, err)
	}
	if len(iface.HardwareAddr) != 6 {
		return nil, fmt.Errorf("llc2: interface %q has no 6-byte MAC", cfg.Interface)
	}

	fd, err := syscall.Socket(afLLC, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("llc2: socket(AF_LLC): %w (is the llc2 kernel module loaded? `modprobe llc2`)", err)
	}

	sa := rawSockaddrLLC{Family: afLLC, Arphrd: arphrdEther, Sap: cfg.LocalSAP}
	copy(sa.Mac[:], iface.HardwareAddr)
	if _, _, errno := syscall.Syscall(syscall.SYS_BIND, uintptr(fd),
		uintptr(unsafe.Pointer(&sa)), unsafe.Sizeof(sa)); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("llc2: bind(%s sap 0x%02X): %w", cfg.Interface, cfg.LocalSAP, errno)
	}
	runtime.KeepAlive(&sa)

	if err := syscall.Listen(fd, 8); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("llc2: listen: %w", err)
	}
	return &llcListener{fd: fd, cfg: cfg}, nil
}

func (l *llcListener) Accept() (Conn, error) {
	var peer rawSockaddrLLC
	plen := uint32(unsafe.Sizeof(peer))
	r1, _, errno := syscall.Syscall6(syscall.SYS_ACCEPT4, uintptr(l.fd),
		uintptr(unsafe.Pointer(&peer)), uintptr(unsafe.Pointer(&plen)), 0, 0, 0)
	if errno != 0 {
		return nil, fmt.Errorf("llc2: accept: %w", errno)
	}
	runtime.KeepAlive(&peer)
	mac := make(net.HardwareAddr, 6)
	copy(mac, peer.Mac[:])
	return &llcConn{fd: int(r1), remoteMAC: mac, remoteSAP: peer.Sap}, nil
}

func (l *llcListener) Close() error { return syscall.Close(l.fd) }

// dial actively opens an LLC2 link to remoteMAC/remoteSAP. The kernel binds the
// local SAP, then connect() drives SABME and blocks until UA (link up) or error.
func dial(cfg Config, remoteMAC net.HardwareAddr, remoteSAP byte) (Conn, error) {
	iface, err := net.InterfaceByName(cfg.Interface)
	if err != nil {
		return nil, fmt.Errorf("llc2: interface %q: %w", cfg.Interface, err)
	}
	if len(iface.HardwareAddr) != 6 {
		return nil, fmt.Errorf("llc2: interface %q has no 6-byte MAC", cfg.Interface)
	}
	if len(remoteMAC) != 6 {
		return nil, fmt.Errorf("llc2: remote MAC must be 6 bytes, got %d", len(remoteMAC))
	}

	fd, err := syscall.Socket(afLLC, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("llc2: socket(AF_LLC): %w (is the llc2 kernel module loaded?)", err)
	}

	local := rawSockaddrLLC{Family: afLLC, Arphrd: arphrdEther, Sap: cfg.LocalSAP}
	copy(local.Mac[:], iface.HardwareAddr)
	if _, _, errno := syscall.Syscall(syscall.SYS_BIND, uintptr(fd),
		uintptr(unsafe.Pointer(&local)), unsafe.Sizeof(local)); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("llc2: bind(%s sap 0x%02X): %w", cfg.Interface, cfg.LocalSAP, errno)
	}
	runtime.KeepAlive(&local)

	remote := rawSockaddrLLC{Family: afLLC, Arphrd: arphrdEther, Sap: remoteSAP}
	copy(remote.Mac[:], remoteMAC)
	if _, _, errno := syscall.Syscall(syscall.SYS_CONNECT, uintptr(fd),
		uintptr(unsafe.Pointer(&remote)), unsafe.Sizeof(remote)); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("llc2: connect(%s sap 0x%02X): %w", remoteMAC, remoteSAP, errno)
	}
	runtime.KeepAlive(&remote)

	mac := make(net.HardwareAddr, 6)
	copy(mac, remoteMAC)
	return &llcConn{fd: fd, remoteMAC: mac, remoteSAP: remoteSAP}, nil
}

// Read returns the next inbound I-frame payload (one SNA PIU). LLC2 is
// frame-oriented, so one Read corresponds to one information frame.
func (c *llcConn) Read() ([]byte, error) {
	buf := make([]byte, 65535)
	n, err := syscall.Read(c.fd, buf)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, io.EOF
	}
	out := make([]byte, n)
	copy(out, buf[:n])
	return out, nil
}

// Write sends one I-frame payload (one SNA PIU). Safe for concurrent use.
func (c *llcConn) Write(piu []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	n, err := syscall.Write(c.fd, piu)
	if err != nil {
		return err
	}
	if n != len(piu) {
		return fmt.Errorf("llc2: short write %d/%d (PIU split across frames)", n, len(piu))
	}
	return nil
}

func (c *llcConn) RemoteMAC() net.HardwareAddr { return c.remoteMAC }
func (c *llcConn) RemoteSAP() byte             { return c.remoteSAP }
func (c *llcConn) Close() error                { return syscall.Close(c.fd) }

// SetReadDeadline implements a read timeout via SO_RCVTIMEO. A subsequent Read
// that times out returns an error wrapping syscall.EAGAIN.
func (c *llcConn) SetReadDeadline(t time.Time) error {
	var tv syscall.Timeval
	if !t.IsZero() {
		d := time.Until(t)
		if d < time.Microsecond {
			d = time.Microsecond // 0 would mean "no timeout"; use the smallest
		}
		tv = syscall.NsecToTimeval(d.Nanoseconds())
	}
	return syscall.SetsockoptTimeval(c.fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
}
