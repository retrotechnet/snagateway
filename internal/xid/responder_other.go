//go:build !linux

package xid

import (
	"errors"
	"net"
)

// ErrNotImplemented is returned on non-Linux platforms (AF_PACKET is Linux-only).
var ErrNotImplemented = errors.New("xid: AF_PACKET responder is Linux-only")

// Responder is a stub on non-Linux platforms.
type Responder struct{}

func NewResponder(ifaceName string, remoteMAC net.HardwareAddr, sap byte) (*Responder, error) {
	return nil, ErrNotImplemented
}

func (r *Responder) Close() error                  { return nil }
func (r *Responder) LocalMAC() net.HardwareAddr     { return nil }
func (r *Responder) Recv() ([]byte, error)          { return nil, ErrNotImplemented }
func (r *Responder) SendXID(info []byte) error      { return ErrNotImplemented }
