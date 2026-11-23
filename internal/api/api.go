// Package api defines the JSON request/response types exchanged between the
// wgent CLI and the wgent service.
package api

import (
	"time"

	"github.com/marr-cloud/wgent/internal/health"
)

const ProtocolVersion = 1

type Op string

const (
	OpList   Op = "list"
	OpStatus Op = "status"
	OpUp     Op = "up"
	OpDown   Op = "down"
)

const (
	CodeUsage               = "usage"
	CodeNoDefault           = "no_default_tunnel"
	CodeInvalidName         = "invalid_tunnel_name"
	CodeServiceNotInstalled = "service_not_installed"
	CodeProtocolMismatch    = "protocol_mismatch"
	CodeTunnelNotFound      = "tunnel_not_found"
	CodeTimeout             = "timeout"
	CodeInternal            = "internal"
)

type Request struct {
	ProtocolVersion int    `json:"protocol_version"`
	Op              Op     `json:"op"`
	Tunnel          string `json:"tunnel,omitempty"`
}

// PeerInfo has nil LastHandshake/HandshakeAgeS when the peer never handshook.
type PeerInfo struct {
	Endpoint      string     `json:"endpoint"`
	LastHandshake *time.Time `json:"last_handshake"`
	HandshakeAgeS *int64     `json:"handshake_age_s"`
	RxBytes       uint64     `json:"rx_bytes"`
	TxBytes       uint64     `json:"tx_bytes"`
}

type Status struct {
	Tunnel  string              `json:"tunnel"`
	State   health.State        `json:"state"`
	Reason  string              `json:"reason"`
	Changed bool                `json:"changed"`
	Service health.ServiceState `json:"service"`
	Peers   []PeerInfo          `json:"peers"`
	Default bool                `json:"default,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (e *Error) Error() string { return e.Message }

type Response struct {
	ProtocolVersion int      `json:"protocol_version"`
	Status          *Status  `json:"status,omitempty"`
	Tunnels         []Status `json:"tunnels,omitempty"`
	Error           *Error   `json:"error,omitempty"`
}
