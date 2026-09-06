package session

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/keys"
	"github.com/nyxveil/nvp/core/nvperr"
	"github.com/nyxveil/nvp/core/packet"
	"github.com/nyxveil/nvp/core/protocol"
	"github.com/nyxveil/nvp/core/replay"
	"github.com/nyxveil/nvp/core/transport"
)

var (
	ErrInvalidState      = errors.New("invalid session state transition")
	ErrNotEstablished    = errors.New("session not established")
	ErrAuthRequired      = errors.New("authentication required")
	ErrReplay            = errors.New("replay detected")
	ErrAEADFailure       = errors.New("aead authentication failed")
	ErrHandshakeTimeout  = errors.New("handshake timeout")
	ErrSequenceExhausted = errors.New("send sequence exhausted")
	ErrEpochExhausted    = errors.New("epoch exhausted")
	ErrRekeyTimeout      = errors.New("rekey ack timeout")
)

// seqExhaustThreshold refuses allocation at MaxUint64-1 so sendSeq never wraps.
const seqExhaustThreshold = math.MaxUint64 - 1

// Config holds session configuration.
type Config struct {
	RekeyInterval     time.Duration
	RekeyPacketCount  uint64
	RekeyByteCount    uint64
	RekeyAckTimeout   time.Duration // 0 = protocol.RekeyTimeout
	ReplayWindow      uint64
	PaddingPolicy     PaddingPolicy
	IsClient          bool
	MTU               int
	KeepaliveInterval time.Duration // 0 = disabled
	KeepaliveJitter   time.Duration // max additive jitter; 0 = DefaultKeepaliveJitter when interval set
	KeepaliveRand     io.Reader     // injectable RNG for tests; nil = crypto/rand
	// ForceStreamData keeps VPN DATA on the reliable stream (tests / constrained peers).
	// Production QUIC prefers DATAGRAM when available.
	ForceStreamData bool
}

// Validate checks session configuration.
//
// Defaults (see protocol.Default*): RekeyInterval 30m, RekeyPacketCount 1e6,
// RekeyByteCount 1GiB, ReplayWindow 1024, padding random-range 0–64 bytes.
// Connector AUTH wait uses protocol.DefaultAuthTimeout (15s) when ConnectConfig.AuthTimeout is 0.
func (c Config) Validate() error {
	if c.MTU < 0 {
		return fmt.Errorf("MTU must be >= 0")
	}
	if c.ReplayWindow == 0 {
		return fmt.Errorf("ReplayWindow must be > 0")
	}
	if c.RekeyInterval < 0 {
		return fmt.Errorf("RekeyInterval must be >= 0")
	}
	if c.RekeyPacketCount == 0 && c.RekeyByteCount == 0 && c.RekeyInterval == 0 {
		return fmt.Errorf("at least one rekey threshold (interval, packet, or byte count) must be set")
	}
	if c.RekeyAckTimeout < 0 {
		return fmt.Errorf("RekeyAckTimeout must be >= 0")
	}
	if c.KeepaliveInterval < 0 || c.KeepaliveJitter < 0 {
		return fmt.Errorf("keepalive interval/jitter must be >= 0")
	}
	return c.PaddingPolicy.Validate()
}

// DefaultConfig returns sensible production defaults.
func DefaultConfig(isClient bool) Config {
	return Config{
		RekeyInterval:     protocol.DefaultRekeyInterval,
		RekeyPacketCount:  protocol.DefaultRekeyPacketCount,
		RekeyByteCount:    protocol.DefaultRekeyByteCount,
		ReplayWindow:      protocol.DefaultReplayWindow,
		PaddingPolicy:     DefaultPaddingPolicy(),
		IsClient:          isClient,
		MTU:               protocol.DefaultMTU,
		KeepaliveInterval: protocol.DefaultKeepaliveInterval,
		KeepaliveJitter:   protocol.DefaultKeepaliveJitter,
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

	prevEpoch    uint32
	prevRecvAEAD *keys.AEADContext
	prevReplay   *replay.Window
	prevDeadline time.Time
	lastPong     time.Time

	// Rekey ECDH exchange
	rekeyPending  *rekeyState
	rekeyWatchGen uint64
	rekeyDeadline time.Time
	rekeyAttempts int
	rekeyLastErr  error
	closedCh      chan struct{}
	closedOnce    sync.Once

	// Callbacks
	onData    func([]byte) error
	onControl func(msgType byte, payload []byte) error
}

// New creates a new session in StateNew.
func New(cfg Config) *Session {
	if cfg.ReplayWindow == 0 {
		cfg.ReplayWindow = protocol.DefaultReplayWindow
	}
	// Zero-value padding is treated as disabled (valid).
	if cfg.PaddingPolicy.Enabled || cfg.PaddingPolicy.Strategy != "" ||
		cfg.PaddingPolicy.MinBytes != 0 || cfg.PaddingPolicy.MaxBytes != 0 {
		if err := cfg.PaddingPolicy.Validate(); err != nil {
			cfg.PaddingPolicy = DefaultPaddingPolicy()
		}
	}
	return &Session{
		cfg:        cfg,
		state:      StateNew,
		recvReplay: replay.NewWindow(cfg.ReplayWindow),
		closedCh:   make(chan struct{}),
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

// Stats is a snapshot of session counters and state for public API consumers.
type Stats struct {
	SendPackets uint64
	SendBytes   uint64
	RecvPackets uint64
	RecvBytes   uint64
	Epoch       uint32
	State       State
}

// Stats returns packets/bytes/epoch/state.
func (s *Session) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		SendPackets: s.sendPackets,
		SendBytes:   s.sendBytes,
		RecvPackets: s.recvPackets,
		RecvBytes:   s.recvBytes,
		Epoch:       s.epoch,
		State:       s.state,
	}
}

// Done returns a channel that is closed when the session reaches CLOSED.
func (s *Session) Done() <-chan struct{} {
	return s.closedCh
}

// WritePacket is an alias of SendData for the frozen public API.
func (s *Session) WritePacket(ctx context.Context, ipPacket []byte) error {
	return s.SendData(ctx, ipPacket)
}

// WaitEstablished runs a temporary stream reader until AUTH_OK (ESTABLISHED),
// AUTH_FAIL, ctx cancel, or close. Uses the reliable stream only (not datagram
// multiplex) so a subsequent caller ReadLoop does not race a zombie stream reader
// on QUIC CONNECT tunnels where control frames share the stream with the reader.
func (s *Session) WaitEstablished(ctx context.Context) error {
	if s.State() == StateEstablished {
		return nil
	}

	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		s.mu.Lock()
		conn := s.conn
		s.mu.Unlock()
		if conn == nil {
			errCh <- ErrNotEstablished
			return
		}
		errCh <- s.readLoopStream(loopCtx, conn)
	}()

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	stopTempRead := func() {
		cancel()
		s.unblockConnRead()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
		s.clearConnReadDeadline()
	}

	for {
		switch s.State() {
		case StateEstablished:
			stopTempRead()
			return nil
		case StateClosed:
			stopTempRead()
			return nvperr.ErrAuthFailed
		}

		select {
		case <-ctx.Done():
			stopTempRead()
			if s.State() == StateEstablished {
				return nil
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nvperr.ErrAuthTimeout
			}
			if errors.Is(ctx.Err(), context.Canceled) {
				return fmt.Errorf("%w: %w", nvperr.ErrAuthTimeout, ctx.Err())
			}
			return nvperr.ErrAuthTimeout
		case err := <-errCh:
			if s.State() == StateEstablished {
				s.clearConnReadDeadline()
				return nil
			}
			return mapAuthWaitErr(err)
		case <-ticker.C:
		}
	}
}

func (s *Session) unblockConnRead() {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		_ = conn.SetReadDeadline(time.Now())
	}
}

func (s *Session) clearConnReadDeadline() {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		_ = conn.SetReadDeadline(time.Time{})
	}
}

func mapAuthWaitErr(err error) error {
	if err == nil {
		return nvperr.ErrAuthFailed
	}
	if errors.Is(err, nvperr.ErrAuthFailed) || errors.Is(err, nvperr.ErrTicketRejected) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, nvperr.ErrAuthTimeout) {
		return nvperr.ErrAuthTimeout
	}
	if errors.Is(err, nvperr.ErrSessionClosed) {
		return err
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return nvperr.ErrAuthTimeout
	}
	// net/http and TLS often surface deadlines as plain "i/o timeout" errors.
	if strings.Contains(err.Error(), "i/o timeout") || strings.Contains(err.Error(), "deadline exceeded") {
		return nvperr.ErrAuthTimeout
	}
	return fmt.Errorf("%w: %w", nvperr.ErrAuthFailed, err)
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

	pad, err := s.buildHandshakePadding()
	if err != nil {
		return err
	}
	init := packet.HandshakeInitPayload{
		Version:      protocol.CurrentVersion,
		ClientPubKey: kp.Public,
		Padding:      pad,
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

	pad, err := s.buildHandshakePadding()
	if err != nil {
		return err
	}
	epoch := uint32(1)
	resp := packet.HandshakeRespPayload{
		Version:      protocol.CurrentVersion,
		ServerPubKey: kp.Public,
		Epoch:        epoch,
		Padding:      pad,
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
	if err := s.applyKeysLocked(sk); err != nil {
		return err
	}
	kp.ZeroPrivate()
	s.lastRekey = time.Now()
	return nil
}

// Transcript returns a copy of the handshake transcript for AUTH binding.
func (s *Session) Transcript() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.transcript))
	copy(out, s.transcript)
	return out
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

	padding, err := s.buildPadding(len(payload))
	if err != nil {
		s.mu.Unlock()
		return err
	}

	inner, err := packet.EncodeInner(msgType, payload, padding)
	if err != nil {
		s.mu.Unlock()
		return err
	}

	seq, err := s.allocateSendSeqLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	ct, err := s.sendAEAD.Seal(s.epoch, seq, inner)
	if err != nil {
		s.mu.Unlock()
		return err
	}

	wire, err := packet.EncodeWireRecord(s.epoch, seq, ct)
	if err != nil {
		s.mu.Unlock()
		return err
	}

	s.sendPackets++
	s.sendBytes += uint64(len(payload))

	rekeyPayload, rkErr := s.maybeStartRekeyLocked()
	if rkErr != nil {
		s.mu.Unlock()
		return rkErr
	}
	conn := s.conn
	forceStream := s.cfg.ForceStreamData
	s.mu.Unlock()

	writeErr := writeWire(ctx, conn, msgType, wire, forceStream)
	if len(rekeyPayload) > 0 {
		go s.sendRekeyAsync(rekeyPayload)
	}
	return writeErr
}

// allocateSendSeqLocked returns the next send sequence or fails closed without wrapping.
func (s *Session) allocateSendSeqLocked() (uint64, error) {
	if s.sendSeq >= seqExhaustThreshold {
		// Cannot safely allocate or send a rekey control frame without wrapping.
		s.failClosedLocked()
		return 0, ErrSequenceExhausted
	}
	seq := s.sendSeq
	s.sendSeq++
	return seq, nil
}

func writeWire(ctx context.Context, conn transport.Conn, msgType byte, wire []byte, forceStream bool) error {
	if conn == nil {
		return ErrNotEstablished
	}
	if msgType == control.TypeData && !forceStream {
		if dg, ok := conn.(transport.DatagramConn); ok && dg.DatagramsEnabled() {
			return dg.WriteDatagram(ctx, wire)
		}
	}
	return conn.Write(ctx, wire)
}

func (s *Session) sendRekeyAsync(payload []byte) {
	timeout := s.rekeyAckTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := s.sendRekeyControl(ctx, control.TypeRekey, payload); err != nil {
		s.mu.Lock()
		s.rekeyLastErr = err
		s.mu.Unlock()
		s.failClosed()
	}
}

// failClosed transitions the session to CLOSING/CLOSED after a fatal send failure.
func (s *Session) failClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failClosedLocked()
}

func (s *Session) failClosedLocked() {
	if s.state == StateClosed {
		return
	}
	if ValidTransition(s.state, StateClosing) {
		_ = s.transition(StateClosing)
	}
	if ValidTransition(s.state, StateClosed) {
		_ = s.transition(StateClosed)
	}
	s.wipeSecretsLocked()
	s.signalClosedLocked()
}

func (s *Session) signalClosedLocked() {
	s.closedOnce.Do(func() {
		close(s.closedCh)
	})
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
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return ErrNotEstablished
	}
	if dg, ok := conn.(transport.DatagramConn); ok && dg.DatagramsEnabled() {
		return s.readLoopMultiplex(ctx, conn, dg)
	}
	return s.readLoopStream(ctx, conn)
}

func (s *Session) readLoopStream(ctx context.Context, conn transport.Conn) error {
	buf := make([]byte, 0, 65536)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		chunk, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		buf = append(buf, chunk...)

		var procErr error
		buf, procErr = s.drainWireBuffer(buf)
		if procErr != nil {
			return procErr
		}
		if len(buf) > protocol.MaxFrameSize*2 {
			return packet.ErrFrameTooLarge
		}
	}
}

type readResult struct {
	data []byte
	err  error
}

func (s *Session) readLoopMultiplex(ctx context.Context, conn transport.Conn, dg transport.DatagramConn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	streamCh := make(chan readResult, 1)
	dgCh := make(chan readResult, 1)

	go func() {
		defer close(streamCh)
		for {
			chunk, err := conn.Read(ctx)
			select {
			case streamCh <- readResult{data: chunk, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer close(dgCh)
		for {
			chunk, err := dg.ReadDatagram(ctx)
			select {
			case dgCh <- readResult{data: chunk, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	buf := make([]byte, 0, 65536)
	streamOpen, dgOpen := true, true
	for streamOpen || dgOpen {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r, ok := <-streamCh:
			if !ok {
				streamOpen = false
				streamCh = nil
				continue
			}
			if r.err != nil {
				cancel()
				return r.err
			}
			buf = append(buf, r.data...)
			var procErr error
			buf, procErr = s.drainWireBuffer(buf)
			if procErr != nil {
				cancel()
				return procErr
			}
			if len(buf) > protocol.MaxFrameSize*2 {
				cancel()
				return packet.ErrFrameTooLarge
			}
		case r, ok := <-dgCh:
			if !ok {
				dgOpen = false
				dgCh = nil
				continue
			}
			if r.err != nil {
				cancel()
				return r.err
			}
			if err := s.processDatagramWire(r.data); err != nil {
				cancel()
				return err
			}
		}
	}
	return nil
}

func (s *Session) drainWireBuffer(buf []byte) ([]byte, error) {
	for {
		record, remaining, err := packet.DecodeWireRecord(buf)
		if err == packet.ErrFrameTooShort {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
		buf = remaining
		if err := s.processRecord(record.Epoch, record.Sequence, record.Ciphertext); err != nil {
			return buf, err
		}
	}
}

func (s *Session) processDatagramWire(data []byte) error {
	record, remaining, err := packet.DecodeWireRecord(data)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return packet.ErrFrameTooLarge
	}
	return s.processRecord(record.Epoch, record.Sequence, record.Ciphertext)
}

func (s *Session) processRecord(epoch uint32, seq uint64, ciphertext []byte) error {
	s.mu.Lock()
	aead, win, err := s.recvContextLocked(epoch)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	inner, err := aead.Open(epoch, seq, ciphertext)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrAEADFailure, err)
	}
	if err := win.CheckAndMark(epoch, seq); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrReplay, err)
	}
	s.recvPackets++
	s.recvBytes += uint64(len(inner))
	s.mu.Unlock()

	hdr, payload, _, err := packet.DecodeInner(inner)
	if err != nil {
		return err
	}
	return s.handleMessage(hdr.MsgType, payload)
}

func (s *Session) recvContextLocked(epoch uint32) (*keys.AEADContext, *replay.Window, error) {
	if s.recvAEAD == nil {
		return nil, nil, ErrAuthRequired
	}
	if epoch == s.epoch {
		return s.recvAEAD, s.recvReplay, nil
	}
	if s.prevRecvAEAD != nil && epoch == s.prevEpoch && s.prevReplay != nil {
		if !s.prevDeadline.IsZero() && time.Now().After(s.prevDeadline) {
			s.prevRecvAEAD = nil
			s.prevReplay = nil
			return nil, nil, fmt.Errorf("%w: previous epoch expired", ErrAEADFailure)
		}
		return s.prevRecvAEAD, s.prevReplay, nil
	}
	return nil, nil, fmt.Errorf("%w: unknown epoch %d", ErrAEADFailure, epoch)
}

func (s *Session) handleMessage(msgType byte, payload []byte) error {
	s.mu.Lock()
	onControl := s.onControl
	onData := s.onData
	s.mu.Unlock()

	if onControl != nil && control.IsControl(msgType) {
		if err := onControl(msgType, payload); err != nil {
			return err
		}
	}

	switch msgType {
	case control.TypeAuth:
		return nil
	case control.TypeAuthOK:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.state == StateAuthenticating {
			return s.transition(StateEstablished)
		}
		return fmt.Errorf("%w: auth_ok in %s", ErrInvalidState, s.state)
	case control.TypeAuthFail:
		s.mu.Lock()
		defer s.mu.Unlock()
		_ = s.transition(StateClosing)
		_ = s.transition(StateClosed)
		s.wipeSecretsLocked()
		s.signalClosedLocked()
		return nvperr.ErrAuthFailed
	case control.TypeData:
		s.mu.Lock()
		ok := s.state.CanSendData()
		s.mu.Unlock()
		if !ok {
			return fmt.Errorf("%w: data before auth", ErrInvalidState)
		}
		if onData != nil {
			return onData(payload)
		}
	case control.TypePing:
		s.mu.Lock()
		if !s.lastPong.IsZero() && time.Since(s.lastPong) < 100*time.Millisecond {
			s.mu.Unlock()
			return nil
		}
		s.lastPong = time.Now()
		s.mu.Unlock()
		go func() {
			_ = s.sendControl(context.Background(), control.TypePong, payload)
		}()
	case control.TypeRekey:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.handleRekeyLocked(payload)
	case control.TypeRekeyAck:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.handleRekeyAckLocked(payload)
	case control.TypeClose:
		s.mu.Lock()
		defer s.mu.Unlock()
		_ = s.transition(StateClosing)
		_ = s.transition(StateClosed)
		s.wipeSecretsLocked()
		s.signalClosedLocked()
		return nvperr.ErrSessionClosed
	}
	return nil
}

func (s *Session) applyKeysLocked(sk *keys.SessionKeys) error {
	var sendAEAD, recvAEAD *keys.AEADContext
	var err error
	if s.cfg.IsClient {
		sendAEAD, err = keys.NewClientAEAD(sk.ClientToServer)
		if err != nil {
			return err
		}
		recvAEAD, err = keys.NewServerAEAD(sk.ServerToClient)
		if err != nil {
			return err
		}
	} else {
		sendAEAD, err = keys.NewServerAEAD(sk.ServerToClient)
		if err != nil {
			return err
		}
		recvAEAD, err = keys.NewClientAEAD(sk.ClientToServer)
		if err != nil {
			return err
		}
	}

	if s.recvAEAD != nil && s.epoch != sk.Epoch {
		s.prevEpoch = s.epoch
		s.prevRecvAEAD = s.recvAEAD
		s.prevReplay = s.recvReplay.Clone()
		s.prevDeadline = time.Now().Add(protocol.RekeyOverlapWindow)
	}

	if s.sessionKeys != nil && s.sessionKeys != sk {
		s.sessionKeys.Zero()
	}

	s.epoch = sk.Epoch
	s.recvReplay = replay.NewWindow(s.cfg.ReplayWindow)
	s.recvReplay.Reset(sk.Epoch)
	s.sendAEAD = sendAEAD
	s.recvAEAD = recvAEAD
	s.sessionKeys = sk
	s.sendSeq = 0
	s.sendPackets = 0
	s.sendBytes = 0
	s.lastRekey = time.Now()
	return nil
}

func (s *Session) wipeSecretsLocked() {
	for i := range s.sharedSecret {
		s.sharedSecret[i] = 0
	}
	if s.sessionKeys != nil {
		s.sessionKeys.Zero()
		s.sessionKeys = nil
	}
	if s.localEphemeral != nil {
		s.localEphemeral.ZeroPrivate()
		s.localEphemeral = nil
	}
	if s.rekeyPending != nil {
		if s.rekeyPending.localEphemeral != nil {
			s.rekeyPending.localEphemeral.ZeroPrivate()
		}
		s.rekeyPending = nil
	}
	s.sendAEAD = nil
	s.recvAEAD = nil
	s.prevRecvAEAD = nil
	s.prevReplay = nil
	s.cancelRekeyWatchdogLocked()
}

func cryptoRandN(n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}
	limit := uint64(n)
	max := (^uint64(0) / limit) * limit
	for {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		v := binary.BigEndian.Uint64(b[:])
		if v < max {
			return int(v % limit), nil
		}
	}
}

// Epoch returns the current session epoch.
func (s *Session) Epoch() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// Close gracefully closes the session.
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == StateClosed {
		s.wipeSecretsLocked()
		s.signalClosedLocked()
		return nil
	}
	if ValidTransition(s.state, StateClosing) {
		_ = s.transition(StateClosing)
	}
	_ = s.transition(StateClosed)
	s.wipeSecretsLocked()
	s.signalClosedLocked()
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
func (s *Session) HandleAuthFail(ctx context.Context, _ byte) error {
	return s.sendControl(ctx, control.TypeAuthFail, []byte{control.AuthFailInvalidTicket})
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
