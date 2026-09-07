package ipc

// Protocol version for named-pipe JSON messages.
const ProtocolVersion = 1

// PipeName is the Windows named pipe path (service creates; GUI connects).
const PipeName = `\\.\pipe\NyxveilClient`

// Message types (GUI ↔ Service). License Credential NEVER appears here.
const (
	TypeHello               = "hello"
	TypeStatus              = "status"
	TypeConnect             = "connect"
	TypeDisconnect          = "disconnect"
	TypeNeedAccessTicket    = "need_access_ticket"
	TypeProvideAccessTicket = "provide_access_ticket"
	TypeAccessTicket        = "access_ticket" // legacy alias accepted as provide
	TypeError               = "error"
	TypeCancel              = "cancel"
	// TypeGateApplyIsolated is test-only: LocalSystem service applies a safe
	// isolated network transaction kept until Disconnect/SCM Stop.
	TypeGateApplyIsolated = "gate_apply_isolated"
	TypeGateClear         = "gate_clear"

	// Diagnostic log stream (ring buffer in service).
	TypeGetLogs         = "get_logs"
	TypeSubscribeLogs   = "subscribe_logs"
	TypeUnsubscribeLogs = "unsubscribe_logs"
	TypeLogsSnapshot    = "logs_snapshot"
	TypeLogEvent        = "log_event"
)

// Envelope is the versioned pipe frame.
type Envelope struct {
	Version int    `json:"v"`
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
}

// ConnectRequest is sent by GUI after CP auth/catalog/ticket. Service re-verifies
// signed catalog with catalog_keys and runs Frozen Connector.OpenSession.
type ConnectRequest struct {
	Envelope
	DesiredLocationID string            `json:"desired_location_id"`
	AccessTicket      string            `json:"access_ticket"`
	SignedCatalogJSON []byte            `json:"signed_catalog_json"`
	CatalogKeys       map[string]string `json:"catalog_keys"` // kid → std Base64 Ed25519 pub
	// DevicePrivateKey is ephemeral Ed25519 private key bytes for AUTH only (in-memory).
	DevicePrivateKey []byte `json:"device_private_key,omitempty"`
	ControlPlaneHost string `json:"control_plane_host,omitempty"` // for bypass route (hostname)
}

// NeedAccessTicket is emitted by Service when reconnect/failover needs a fresh ticket.
type NeedAccessTicket struct {
	Envelope
	RequestID         string `json:"request_id"`
	DesiredLocationID string `json:"location_id"`
	Reason            string `json:"reason"`
	State             string `json:"state"`
}

// ProvideAccessTicket is GUI → Service reply (license stays in CurrentUser process).
type ProvideAccessTicket struct {
	Envelope
	RequestID    string `json:"request_id"`
	AccessTicket string `json:"access_ticket"`
}

// AccessTicketResponse is legacy GUI → Service reply; prefer ProvideAccessTicket.
type AccessTicketResponse struct {
	Envelope
	RequestID         string `json:"request_id,omitempty"`
	DesiredLocationID string `json:"desired_location_id,omitempty"`
	AccessTicket      string `json:"access_ticket"`
}

// StatusSnapshot is authoritative Service state for GUI mirroring.
// Telemetry fields (vpn_ip, bytes, connected_at) are read-only GUI mirrors —
// they do not alter the data plane.
type StatusSnapshot struct {
	Envelope
	State            string   `json:"state"`
	LocationID       string   `json:"location_id,omitempty"`
	NodeID           string   `json:"node_id,omitempty"`
	Transport        string   `json:"transport,omitempty"`
	LastError        string   `json:"last_error,omitempty"`
	ClientVer        string   `json:"client_version,omitempty"`
	CoreVer          string   `json:"core_version,omitempty"`
	Protocol         string   `json:"protocol,omitempty"`
	VpnIP            string   `json:"vpn_ip,omitempty"`
	DNSServers       []string `json:"dns_servers,omitempty"`
	ConnectedAtUnix  int64    `json:"connected_at_unix,omitempty"`
	TxBytes          uint64   `json:"tx_bytes,omitempty"`
	RxBytes          uint64   `json:"rx_bytes,omitempty"`
	EffectiveMTU     int      `json:"effective_mtu,omitempty"`
}

// LogLine is one diagnostic event for GUI (pre-formatted + structured fields).
type LogLine struct {
	Time      string `json:"time"`
	Level     string `json:"level"`
	Component string `json:"component"`
	Event     string `json:"event"`
	Message   string `json:"message,omitempty"`
	Line      string `json:"line"`
}

// LogsSnapshot is the backlog reply to get_logs / subscribe_logs.
type LogsSnapshot struct {
	Envelope
	Entries []LogLine `json:"entries"`
}

// LogEventMessage is a live diagnostic line pushed to subscribed GUI clients.
type LogEventMessage struct {
	Envelope
	Entry LogLine `json:"entry"`
}
