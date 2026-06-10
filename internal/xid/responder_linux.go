//go:build linux

package xid

import (
	"fmt"
	"net"
	"syscall"
)

const ethP8022 = 0x0004 // ETH_P_802_2: 802.3 length-framed LLC frames

func htons(x uint16) uint16 { return x<<8 | x>>8 }

// Responder sends/receives raw 802.3 LLC frames on an interface via AF_PACKET,
// so we can answer SNA Server's XID with an SNA XID3 (which the kernel can't).
type Responder struct {
	fd        int
	ifi       *net.Interface
	localMAC  net.HardwareAddr
	remoteMAC net.HardwareAddr
	sap       byte
}

// NewResponder opens an AF_PACKET socket bound to ifaceName for 802.3 LLC frames.
func NewResponder(ifaceName string, remoteMAC net.HardwareAddr, sap byte) (*Responder, error) {
	ifi, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, fmt.Errorf("xid: interface %q: %w", ifaceName, err)
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(ethP8022)))
	if err != nil {
		return nil, fmt.Errorf("xid: AF_PACKET socket: %w", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(ethP8022), Ifindex: ifi.Index}); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("xid: bind %s: %w", ifaceName, err)
	}
	return &Responder{fd: fd, ifi: ifi, localMAC: ifi.HardwareAddr, remoteMAC: remoteMAC, sap: sap}, nil
}

// Close closes the socket.
func (r *Responder) Close() error { return syscall.Close(r.fd) }

// LocalMAC returns the interface's hardware address.
func (r *Responder) LocalMAC() net.HardwareAddr { return r.localMAC }

// Recv reads the next raw Ethernet frame (full frame incl. 14-byte header).
func (r *Responder) Recv() ([]byte, error) {
	buf := make([]byte, 1600)
	n, _, err := syscall.Recvfrom(r.fd, buf, 0)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// SendXID transmits an XID response (Final bit set) to the remote with the given
// XID3 information field.
func (r *Responder) SendXID(info []byte) error {
	// DSAP = remote SAP, SSAP = our SAP with C/R=1 (response), control = XID+F.
	llc := []byte{r.sap, r.sap | 0x01, 0xBF}
	payload := append(llc, info...)
	frame := make([]byte, 0, 14+len(payload))
	frame = append(frame, r.remoteMAC...)
	frame = append(frame, r.localMAC...)
	frame = append(frame, byte(len(payload)>>8), byte(len(payload))) // 802.3 length
	frame = append(frame, payload...)
	for len(frame) < 60 { // pad to Ethernet minimum
		frame = append(frame, 0)
	}
	sa := &syscall.SockaddrLinklayer{Ifindex: r.ifi.Index, Halen: 6}
	copy(sa.Addr[:], r.remoteMAC)
	return syscall.Sendto(r.fd, frame, 0, sa)
}
