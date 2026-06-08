// Package config defines the gateway configuration model: how each SNA Server
// connection (PU) and its LU2s map onto TN3270 back-end targets.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config is the top-level gateway configuration.
type Config struct {
	LLC2          LLC2Config   `json:"llc2"`
	DefaultTarget Target       `json:"default_target"`
	Connections   []Connection `json:"connections"`
}

// LLC2Config describes the gateway's own 802.2 link toward SNA Server.
type LLC2Config struct {
	Interface string `json:"interface"` // Linux NIC, e.g. "eth0"
	LocalSAP  string `json:"local_sap"` // hex string, e.g. "0x04"
}

// Connection is one SNA Server PU reachable over 802.2/LLC2.
type Connection struct {
	Name      string `json:"name"`
	RemoteMAC string `json:"remote_mac"` // SNA Server NIC MAC
	RemoteSAP string `json:"remote_sap"` // hex string, e.g. "0x04"
	LUs       []LU   `json:"lus"`
}

// LU maps a single dependent LU2 to a TN3270 back-end target.
type LU struct {
	LU     int    `json:"lu"`     // local address / LU number on the PU
	Target Target `json:"target"` // back-end host this LU's sessions bridge to
}

// Target is a TN3270 host endpoint.
type Target struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Model   string `json:"model"`   // e.g. "IBM-3278-2"
	TN3270E bool   `json:"tn3270e"` // use TN3270E (RFC 2355) negotiation
}

// Addr returns host:port for dialing.
func (t Target) Addr() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// SAP parses LocalSAP into a byte.
func (l LLC2Config) SAP() (byte, error) { return parseSAP(l.LocalSAP) }

// SAP parses RemoteSAP into a byte.
func (c Connection) SAP() (byte, error) { return parseSAP(c.RemoteSAP) }

// HardwareAddr parses RemoteMAC.
func (c Connection) HardwareAddr() (net.HardwareAddr, error) {
	return net.ParseMAC(c.RemoteMAC)
}

func parseSAP(s string) (byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	v, err := strconv.ParseUint(s, 16, 8)
	if err != nil {
		return 0, fmt.Errorf("invalid SAP %q: %w", s, err)
	}
	return byte(v), nil
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// TargetForLU returns the configured back-end for a given connection/LU,
// falling back to DefaultTarget.
func (c *Config) TargetForLU(connName string, lu int) Target {
	for _, conn := range c.Connections {
		if conn.Name != connName {
			continue
		}
		for _, l := range conn.LUs {
			if l.LU == lu {
				return c.resolve(l.Target)
			}
		}
	}
	return c.DefaultTarget
}

// resolve fills empty fields of a target from DefaultTarget.
func (c *Config) resolve(t Target) Target {
	if t.Host == "" {
		t.Host = c.DefaultTarget.Host
	}
	if t.Port == 0 {
		t.Port = c.DefaultTarget.Port
	}
	if t.Model == "" {
		t.Model = c.DefaultTarget.Model
	}
	return t
}

// Validate performs basic sanity checks.
func (c *Config) Validate() error {
	if c.LLC2.Interface == "" {
		return fmt.Errorf("llc2.interface is required")
	}
	if _, err := c.LLC2.SAP(); err != nil {
		return fmt.Errorf("llc2.local_sap: %w", err)
	}
	if c.DefaultTarget.Host == "" {
		return fmt.Errorf("default_target.host is required")
	}
	if c.DefaultTarget.Port == 0 {
		c.DefaultTarget.Port = 3270
	}
	if c.DefaultTarget.Model == "" {
		c.DefaultTarget.Model = "IBM-3278-2"
	}
	seen := map[string]bool{}
	for i, conn := range c.Connections {
		if conn.Name == "" {
			return fmt.Errorf("connections[%d]: name is required", i)
		}
		if seen[conn.Name] {
			return fmt.Errorf("duplicate connection name %q", conn.Name)
		}
		seen[conn.Name] = true
		if _, err := conn.HardwareAddr(); err != nil {
			return fmt.Errorf("connections[%d] (%s): remote_mac: %w", i, conn.Name, err)
		}
		if _, err := conn.SAP(); err != nil {
			return fmt.Errorf("connections[%d] (%s): remote_sap: %w", i, conn.Name, err)
		}
		luSeen := map[int]bool{}
		for _, l := range conn.LUs {
			if luSeen[l.LU] {
				return fmt.Errorf("connection %s: duplicate LU %d", conn.Name, l.LU)
			}
			luSeen[l.LU] = true
		}
	}
	return nil
}
