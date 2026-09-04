package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	mathrand "math/rand"
	"sync"
	"time"

	"github.com/nyxveil/nvp/control"
	"github.com/nyxveil/nvp/keys"
	"github.com/nyxveil/nvp/packet"
	"github.com/nyxveil/nvp/protocol"
	"github.com/nyxveil/nvp/replay"
	"github.com/nyxveil/nvp/transport"
)

var (
	ErrInvalidState     = errors.New("invalid session state transition")
	ErrNotEstablished   = errors.New("session not established")
	ErrAuthRequired     = errors.New("authentication required")
	ErrReplay           = errors.New("replay detected")
	ErrAEADFailure      = errors.New("aead authentication failed")
	ErrHandshakeTimeout = errors.New("handshake timeout")
)

// Config holds session configuration.
type Config struct {
	RekeyInterval    time.Duration
	RekeyPacketCount uint64
	RekeyByteCount   uint64
	ReplayWindow     uint64
	PaddingPolicy    PaddingPolicy
	IsClient         bool
	MTU              int
}

// PaddingPolicy controls optional authenticated padding.
type PaddingPolicy struct {
	Enabled     bool
	MinBytes    int
	MaxBytes    int
	Probability float64 // 0.0-1.0 chance to pad a frame
}

// DefaultConfig returns sensible defaults.
func DefaultConfig(isClient bool) Config {
	return Config{
		RekeyInterval:    protocol.DefaultRekeyInterval,
		RekeyPacketCount: protocol.DefaultRekeyPacketCount,
		RekeyByteCount:   protocol.DefaultRekeyByteCount,
		ReplayWindow:     protocol.DefaultReplayWindow,
		IsClient:         isClient,
		MTU:              protocol.DefaultMTU,
	}
}

// Session is an NVP/1 VPN session over an abstract transport.
type Session struct {
	mu sync.Mutex

	cfg   Config
	state State
	conn  transport.Conn

	// Crypto state
	epoch       uint32
	sendSeq     uint64
	recvReplay  *replay.Window
	sendAEAD    *keys.AEADContext
	recvAEAD    *keys.AEADContext
	sessionKeys *keys.SessionKeys

	// Rekey counters
	sendPackets uint64
	sendBytes   uint64
	recvPackets uint64
	recvBytes   uint64
	lastRekey   time.Time

	// Handshake
	localEphemeral *keys.EphemeralKeypair
	peerPubKey     [32]byte
	transcript     []byte
	sharedSecret   [32]byte

	// Rekey ECDH exchange
	rekeyPending *rekeyState

	// Callbacks
	onData    func([]byte) error
	onControl func(msgType byte, payload []byte) error
}

// New creates a new session in StateNew.
func New(cfg Config) *Session {
	return &Session{
		cfg:        cfg,
		state:      StateNew,
		recvReplay: replay.NewWindow(cfg.ReplayWindow),
	}
}

// Connect establishes transport and moves to TRANSPORT_CONNECTED.
func (s *Session) Connect(ctx context.Context, conn transport.Conn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateNew {
		return fmt.Errorf("%w: connect from %s", ErrInvalidState, s.state)
	}
	s.conn = conn
	s.state = StateTransportConnected
	return nil
}

// OnControl sets callback for control messages after decryption.
func (s *Session) OnControl(fn func(msgType byte, payload []byte) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onControl = fn
}

// OnData sets callback for received IP packets.
func (s *Session) OnData(fn func([]byte) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onData = fn
}

// State returns current session state.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// transition moves to new state if valid.
func (s *Session) transition(to State) error {
	if !ValidTransition(s.state, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidState, s.state, to)
	}
	s.state = to
	return nil
}

// RunHandshake performs X25519 handshake inside TLS-protected transport.
func (s *Session) RunHandshake(ctx context.Context) error {
	s.mu.Lock()
	if s.state != StateTransportConnected {
		s.mu.Unlock()
		return fmt.Errorf("%w: handshake from %s", ErrInvalidState, s.state)
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, protocol.HandshakeTimeout)
	defer cancel()

	var err error
	if s.cfg.IsClient {
		err = s.clientHandshake(ctx)
	} else {
		err = s.serverHandshake(ctx)
	}
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.transition(StateSecureChannel); err != nil {
		return err
	}
	return s.transition(StateAuthenticating)
}

func (s *Session) clientHandshake(ctx context.Context) error {
	kp, err := keys.GenerateEphemeral()
	if err != nil {
		return err
	}

	init := packet.HandshakeInitPayload{
		Version:      protocol.CurrentVersion,
		ClientPubKey: kp.Public,
	}
	initBytes, err := packet.EncodeHandshakeInit(init)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.localEphemeral = kp
	s.mu.Unlock()

	if err := s.conn.Write(ctx, initBytes); err != nil {
		return fmt.Errorf("write handshake init: %w", err)
	}

	respBytes, err := s.conn.Read(ctx)
	if err != nil {
		return fmt.Errorf("read handshake resp: %w", err)
	}
	resp, err := packet.DecodeHandshakeResp(respBytes)
	if err != nil {
		return err
	}
	if resp.Version < protocol.MinVersion || resp.Version > protocol.MaxVersion {
		return fmt.Errorf("unsupported protocol version: %d", resp.Version)
	}

	return s.finishHandshake(kp, resp.ServerPubKey, resp.Epoch, initBytes, respBytes)
}

func (s *Session) serverHandshake(ctx context.Context) error {
	initBytes, err := s.conn.Read(ctx)
	if err != nil {
		return fmt.Errorf("read handshake init: %w", err)
	}
	init, err := packet.DecodeHandshakeInit(initBytes)
	if err != nil {
		return err
	}
	if init.Version < protocol.MinVersion || init.Version > protocol.MaxVersion {
		return fmt.Errorf("unsupported protocol version: %d", init.Version)
	}

	kp, err := keys.GenerateEphemeral()
	if err != nil {
		return err
	}

	epoch := uint32(1)
	resp := packet.HandshakeRespPayload{
		Version:      protocol.CurrentVersion,
		ServerPubKey: kp.Public,
		Epoch:        epoch,
	}
	respBytes, err := packet.EncodeHandshakeResp(resp)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.localEphemeral = kp
	s.mu.Unlock()

	if err := s.conn.Write(ctx, respBytes); err != nil {
		return fmt.Errorf("write handshake resp: %w", err)
	}

	return s.finishHandshake(kp, init.ClientPubKey, epoch, initBytes, respBytes)
}

func (s *Session) finishHandshake(kp *keys.EphemeralKeypair, peerPub [32]byte, epoch uint32, initBytes, respBytes []byte) error {
	shared, err := keys.SharedSecret(&kp.Private, &peerPub)
	if err != nil {
		return err
	}

	transcript := append(append([]byte{}, initBytes...), respBytes...)
	sk, err := keys.DeriveSessionKeys(shared, transcript, epoch)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.peerPubKey = peerPub
	s.transcript = transcript
	s.sharedSecret = shared
	s.sessionKeys = sk
	s.epoch = epoch
	s.recvReplay.Reset(epoch)

	if s.cfg.IsClient {
		sendAEAD, err := keys.NewClientAEAD(sk.ClientToServer)
		if err != nil {
			return err
		}
		recvAEAD, err := keys.NewServerAEAD(sk.ServerToClient)
		if err != nil {
			return err
		}
		s.sendAEAD = sendAEAD
		s.recvAEAD = recvAEAD
	} else {
		sendAEAD, err := keys.NewServerAEAD(sk.ServerToClient)
		if err != nil {
			return err
		}
		recvAEAD, err := keys.NewClientAEAD(sk.ClientToServer)
		if err != nil {
			return err
		}
		s.sendAEAD = sendAEAD
		s.recvAEAD = recvAEAD
	}
	s.lastRekey = time.Now()
	return nil
}

// SendAuth sends AUTH control message with ticket payload.
func (s *Session) SendAuth(ctx context.Context, ticketPayload []byte) error {
	return s.sendControl(ctx, control.TypeAuth, ticketPayload)
}

// SendData sends an IP packet through the tunnel.
func (s *Session) SendData(ctx context.Context, ipPacket []byte) error {
	s.mu.Lock()
	if !s.state.CanSendData() {
		s.mu.Unlock()
		return ErrNotEstablished
	}
	s.mu.Unlock()
	return s.sendEncrypted(ctx, control.TypeData, ipPacket)
}

// SendPing sends a PING control message.
func (s *Session) SendPing(ctx context.Context) error {
	return s.sendControl(ctx, control.TypePing, nil)
}

func (s *Session) sendControl(ctx context.Context, msgType byte, payload []byte) error {
	s.mu.Lock()
	if msgType == control.TypeAuth && !s.state.CanSendAuth() {
		s.mu.Unlock()
		return fmt.Errorf("%w: auth in state %s", ErrInvalidState, s.state)
	}
	s.mu.Unlock()
	return s.sendEncrypted(ctx, msgType, payload)
}

func (s *Session) sendEncrypted(ctx context.Context, msgType byte, payload []byte) error {
	s.mu.Lock()

	if s.sendAEAD == nil {
		s.mu.Unlock()
		return ErrAuthRequired
	}

	var padding []byte
	if s.cfg.PaddingPolicy.Enabled && s.cfg.PaddingPolicy.MaxBytes > 0 {
		prob := s.cfg.PaddingPolicy.Probability
		if prob <= 0 {
			prob = 0.25
		}
		if mathrand.Float64() < prob {
			minB := s.cfg.PaddingPolicy.MinBytes
			maxB := s.cfg.PaddingPolicy.MaxBytes
			if maxB > minB {
				n := minB + mathrand.Intn(maxB-minB+1)
				padding = make([]byte, n)
				_, _ = rand.Read(padding)
			}
		}
	}

	inner, err := packet.EncodeInner(msgType, payload, padding)
	if err != nil {
		s.mu.Unlock()
		return err
	}

	seq := s.sendSeq
	s.sendSeq++
	ct, err := s.sendAEAD.Seal(s.epoch, seq, inner)
	if err != nil {
		s.mu.Unlock()
		return err
	}

	wire, err := packet.EncodeWireRecord(ct)
	if err != nil {
		s.mu.Unlock()
		return err
	}

	s.sendPackets++
	s.sendBytes += uint64(len(payload))

	var rekeyPayload []byte
	if rkPayload, rkErr := s.maybeStartRekeyLocked(); rkErr != nil {
		s.mu.Unlock()
		return rkErr
	} else {
		rekeyPayload = rkPayload
	}

	writeErr := s.conn.Write(ctx, wire)
	s.mu.Unlock()

	if len(rekeyPayload) > 0 {
		go func(p []byte) {
			_ = s.sendRekeyControl(context.Background(), control.TypeRekey, p)
		}(rekeyPayload)
	}
	return writeErr
}

func (s *Session) maybeStartRekeyLocked() ([]byte, error) {
	if !s.state.CanRekey() && s.state != StateRekeying {
		return nil, nil
	}
	need := false
	if time.Since(s.lastRekey) > s.cfg.RekeyInterval {
		need = true
	}
	if s.sendPackets >= s.cfg.RekeyPacketCount {
		need = true
	}
	if s.sendBytes >= s.cfg.RekeyByteCount {
		need = true
	}
	if need && s.state == StateEstablished {
		return s.prepareRekeyLocked()
	}
	return nil, nil
}

// ReadLoop processes incoming transport records until context cancelled.
func (s *Session) ReadLoop(ctx context.Context) error {
	buf := make([]byte, 0, 65536)
	tmp := make([]byte, 8192)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		chunk, err := s.conn.Read(ctx)
		if err != nil {
			return err
		}
		buf = append(buf, chunk...)

		for {
			record, remaining, err := packet.DecodeWireRecord(buf)
			if err == packet.ErrFrameTooShort {
				break
			}
			if err != nil {
				return err
			}
			buf = remaining

			if err := s.processRecord(record.Ciphertext); err != nil {
				return err
			}
		}

		if len(buf) > protocol.MaxFrameSize*2 {
			return packet.ErrFrameTooLarge
		}
		_ = tmp
	}
}

func (s *Session) processRecord(ciphertext []byte) error {
	s.mu.Lock()
	if s.recvAEAD == nil {
		s.mu.Unlock()
		return ErrAuthRequired
	}

	expectedSeq := s.recvPackets
	inner, err := s.recvAEAD.Open(s.epoch, expectedSeq, ciphertext)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrAEADFailure, err)
	}

	if err := s.recvReplay.CheckAndMark(s.epoch, expectedSeq); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrReplay, err)
	}
	s.recvPackets++
	s.mu.Unlock()

	hdr, payload, _, err := packet.DecodeInner(inner)
	if err != nil {
		return err
	}

	return s.handleMessage(hdr.MsgType, payload)
}

func (s *Session) handleMessage(msgType byte, payload []byte) error {
	if s.onControl != nil && control.IsControl(msgType) {
		if err := s.onControl(msgType, payload); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch msgType {
	case control.TypeAuth:
		return nil
	case control.TypeAuthOK:
		if s.state == StateAuthenticating {
			return s.transition(StateEstablished)
		}
		return fmt.Errorf("%w: auth_ok in %s", ErrInvalidState, s.state)
	case control.TypeAuthFail:
		_ = s.transition(StateClosing)
		_ = s.transition(StateClosed)
		return fmt.Errorf("authentication failed")
	case control.TypeData:
		if !s.state.CanSendData() {
			return fmt.Errorf("%w: data before auth", ErrInvalidState)
		}
		if s.onData != nil {
			return s.onData(payload)
		}
	case control.TypePing:
		go func() {
			_ = s.sendControl(context.Background(), control.TypePong, payload)
		}()
	case control.TypeRekey:
		return s.handleRekeyLocked(payload)
	case control.TypeRekeyAck:
		return s.handleRekeyAckLocked(payload)
	case control.TypeClose:
		_ = s.transition(StateClosing)
		_ = s.transition(StateClosed)
	}
	return nil
}

func (s *Session) applyKeysLocked(sk *keys.SessionKeys) {
	oldWindow := s.recvReplay
	s.epoch = sk.Epoch
	s.recvReplay.Reset(sk.Epoch)
	s.recvReplay.BeginTransition(oldWindow.Epoch(), oldWindow)

	if s.cfg.IsClient {
		sendAEAD, _ := keys.NewClientAEAD(sk.ClientToServer)
		recvAEAD, _ := keys.NewServerAEAD(sk.ServerToClient)
		s.sendAEAD = sendAEAD
		s.recvAEAD = recvAEAD
	} else {
		sendAEAD, _ := keys.NewServerAEAD(sk.ServerToClient)
		recvAEAD, _ := keys.NewClientAEAD(sk.ClientToServer)
		s.sendAEAD = sendAEAD
		s.recvAEAD = recvAEAD
	}
	s.sessionKeys = sk
	s.sendSeq = 0
	s.recvPackets = 0
	s.lastRekey = time.Now()
}

// Close gracefully closes the session.
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == StateClosed {
		return nil
	}
	if ValidTransition(s.state, StateClosing) {
		_ = s.transition(StateClosing)
	}
	_ = s.transition(StateClosed)
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// HandleAuthOK marks session as established (server-side after ticket validation).
func (s *Session) HandleAuthOK(ctx context.Context) error {
	return s.sendControl(ctx, control.TypeAuthOK, nil)
}

// HandleAuthFail sends auth failure response.
func (s *Session) HandleAuthFail(ctx context.Context, reason byte) error {
	return s.sendControl(ctx, control.TypeAuthFail, []byte{reason})
}

// MarkEstablished transitions to established (server after local auth).
func (s *Session) MarkEstablished() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateAuthenticating {
		return fmt.Errorf("%w: establish from %s", ErrInvalidState, s.state)
	}
	return s.transition(StateEstablished)
}
