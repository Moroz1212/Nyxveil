package state

import (
	"fmt"
	"sync"
)

// State is the authoritative connection state machine (owned by the Service).
type State int

const (
	Disconnected State = iota
	ValidatingLicense
	FetchingCatalog
	SelectingNode
	RequestingTicket
	ConnectingTransport
	Authenticating
	WaitingForConfig
	ConfiguringTunnel
	Connected
	Reconnecting
	Disconnecting
	Error
)

func (s State) String() string {
	switch s {
	case Disconnected:
		return "Disconnected"
	case ValidatingLicense:
		return "Validating"
	case FetchingCatalog:
		return "Preparing"
	case SelectingNode:
		return "Preparing"
	case RequestingTicket:
		return "Preparing"
	case ConnectingTransport:
		return "ConnectingTransport"
	case Authenticating:
		return "Authenticating"
	case WaitingForConfig:
		return "WaitingForConfig"
	case ConfiguringTunnel:
		return "ConfiguringTunnel"
	case Connected:
		return "Connected"
	case Reconnecting:
		return "Reconnecting"
	case Disconnecting:
		return "Disconnecting"
	case Error:
		return "Error"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// Machine is thread-safe; GUI mirrors Get() only.
type Machine struct {
	mu     sync.RWMutex
	cur    State
	errMsg string
}

func New() *Machine { return &Machine{cur: Disconnected} }

func (m *Machine) Get() (State, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cur, m.errMsg
}

func (m *Machine) Set(s State) {
	m.mu.Lock()
	m.cur = s
	// Clear LastError only on a successful new Connected so the GUI can still
	// show why the prior session died after Disconnected/Reconnecting.
	if s == Connected {
		m.errMsg = ""
	}
	m.mu.Unlock()
}

// SetDetail transitions state while keeping a user-visible LastError (e.g. Reconnecting).
func (m *Machine) SetDetail(s State, detail string) {
	m.mu.Lock()
	m.cur = s
	m.errMsg = detail
	m.mu.Unlock()
}

func (m *Machine) Fail(msg string) {
	m.mu.Lock()
	m.cur = Error
	m.errMsg = msg
	m.mu.Unlock()
}
