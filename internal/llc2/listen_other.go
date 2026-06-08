//go:build !linux

package llc2

import "net"

// AF_LLC is Linux-only. On other platforms (e.g. a Windows authoring machine)
// the gateway still builds, but the LLC2 front end is unavailable.

func listen(cfg Config) (Listener, error) {
	return nil, ErrNotImplemented
}

func dial(cfg Config, remoteMAC net.HardwareAddr, remoteSAP byte) (Conn, error) {
	return nil, ErrNotImplemented
}
