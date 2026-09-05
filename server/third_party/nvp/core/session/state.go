package session

// State represents NVP session state machine states.
type State int

const (
	StateNew State = iota
	StateTransportConnected
	StateSecureChannel
	StateAuthenticating
	StateEstablished
	StateRekeying
	StateClosing
	StateClosed
)

func (s State) String() string {
	switch s {
	case StateNew:
		return "NEW"
	case StateTransportConnected:
		return "TRANSPORT_CONNECTED"
	case StateSecureChannel:
		return "SECURE_CHANNEL"
	case StateAuthenticating:
		return "AUTHENTICATING"
	case StateEstablished:
		return "ESTABLISHED"
	case StateRekeying:
		return "REKEYING"
	case StateClosing:
		return "CLOSING"
	case StateClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}

// ValidTransition checks whether state transition is allowed.
func ValidTransition(from, to State) bool {
	switch from {
	case StateNew:
		return to == StateTransportConnected || to == StateClosed
	case StateTransportConnected:
		return to == StateSecureChannel || to == StateClosing || to == StateClosed
	case StateSecureChannel:
		return to == StateAuthenticating || to == StateClosing || to == StateClosed
	case StateAuthenticating:
		return to == StateEstablished || to == StateClosing || to == StateClosed
	case StateEstablished:
		return to == StateRekeying || to == StateClosing || to == StateClosed
	case StateRekeying:
		return to == StateEstablished || to == StateClosing || to == StateClosed
	case StateClosing:
		return to == StateClosed
	case StateClosed:
		return false
	default:
		return false
	}
}

// CanSendData returns true if DATA messages are permitted.
func (s State) CanSendData() bool {
	return s == StateEstablished || s == StateRekeying
}

// CanSendAuth returns true if AUTH is permitted.
func (s State) CanSendAuth() bool {
	return s == StateAuthenticating
}

// CanRekey returns true if rekey is permitted.
func (s State) CanRekey() bool {
	return s == StateEstablished
}
