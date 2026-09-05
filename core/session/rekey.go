package session

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"time"

	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/keys"
	"github.com/nyxveil/nvp/core/packet"
	"github.com/nyxveil/nvp/core/protocol"
)

type rekeyState struct {
	newEpoch       uint32
	localEphemeral *keys.EphemeralKeypair
	peerPubKey     [32]byte
	initiated      bool
}

func (s *Session) rekeyAckTimeout() time.Duration {
	if s.cfg.RekeyAckTimeout > 0 {
		return s.cfg.RekeyAckTimeout
	}
	return protocol.RekeyTimeout
}

func (s *Session) prepareRekeyLocked() ([]byte, error) {
	if s.epoch == math.MaxUint32 {
		s.failClosedLocked()
		return nil, ErrEpochExhausted
	}
	kp, err := keys.GenerateEphemeral()
	if err != nil {
		return nil, err
	}
	newEpoch := s.epoch + 1
	s.rekeyPending = &rekeyState{
		newEpoch:       newEpoch,
		localEphemeral: kp,
		initiated:      true,
	}
	if err := s.transition(StateRekeying); err != nil {
		return nil, err
	}
	s.rekeyAttempts = 0
	s.startRekeyWatchdogLocked()
	return packet.EncodeRekeyInit(packet.RekeyInitPayload{
		Epoch:        newEpoch,
		EphemeralPub: kp.Public,
	})
}

func (s *Session) startRekeyWatchdogLocked() {
	s.rekeyWatchGen++
	gen := s.rekeyWatchGen
	timeout := s.rekeyAckTimeout()
	s.rekeyDeadline = time.Now().Add(timeout)
	go s.rekeyWatchdog(gen, timeout)
}

func (s *Session) cancelRekeyWatchdogLocked() {
	s.rekeyWatchGen++
	s.rekeyDeadline = time.Time{}
}

func (s *Session) rekeyWatchdog(gen uint64, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-s.closedCh:
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.rekeyWatchGen {
		return
	}
	if s.state != StateRekeying {
		return
	}

	if s.rekeyAttempts == 0 && s.rekeyPending != nil && s.rekeyPending.initiated {
		s.rekeyAttempts = 1
		payload, err := packet.EncodeRekeyInit(packet.RekeyInitPayload{
			Epoch:        s.rekeyPending.newEpoch,
			EphemeralPub: s.rekeyPending.localEphemeral.Public,
		})
		if err != nil {
			s.failClosedLocked()
			s.rekeyLastErr = err
			return
		}
		s.startRekeyWatchdogLocked()
		go s.sendRekeyAsync(payload)
		return
	}

	s.rekeyLastErr = ErrRekeyTimeout
	s.failClosedLocked()
}

func (s *Session) sendRekeyControl(ctx context.Context, msgType byte, payload []byte) error {
	s.mu.Lock()
	if s.sendAEAD == nil {
		s.mu.Unlock()
		return ErrAuthRequired
	}
	seq, err := s.allocateSendSeqLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	aead := s.sendAEAD
	epoch := s.epoch
	s.mu.Unlock()
	return s.sendSealed(ctx, aead, epoch, seq, msgType, payload)
}

func (s *Session) sendSealed(ctx context.Context, aead *keys.AEADContext, epoch uint32, seq uint64, msgType byte, payload []byte) error {
	if aead == nil {
		return ErrAuthRequired
	}
	inner, err := packet.EncodeInner(msgType, payload, nil)
	if err != nil {
		return err
	}
	ct, err := aead.Seal(epoch, seq, inner)
	if err != nil {
		return err
	}
	wire, err := packet.EncodeWireRecord(epoch, seq, ct)
	if err != nil {
		return err
	}
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return ErrNotEstablished
	}
	return conn.Write(ctx, wire)
}

func (s *Session) handleRekeyLocked(payload []byte) error {
	if s.state != StateEstablished && s.state != StateRekeying {
		return fmt.Errorf("%w: rekey in %s", ErrInvalidState, s.state)
	}

	init, err := packet.DecodeRekeyInit(payload)
	if err != nil {
		return err
	}
	if init.Epoch != s.epoch+1 {
		return fmt.Errorf("unexpected rekey epoch %d want %d", init.Epoch, s.epoch+1)
	}

	if s.rekeyPendingInitiated() {
		localPub := s.rekeyPending.localEphemeral.Public
		cmp := bytes.Compare(init.EphemeralPub[:], localPub[:])
		if cmp < 0 || (cmp == 0 && s.cfg.IsClient) {
			return nil
		}
		s.rekeyPending.localEphemeral.ZeroPrivate()
		s.rekeyPending = nil
		s.cancelRekeyWatchdogLocked()
		if s.state == StateRekeying {
			s.state = StateEstablished
		}
	}

	kp, err := keys.GenerateEphemeral()
	if err != nil {
		return err
	}
	s.rekeyPending = &rekeyState{
		newEpoch:       init.Epoch,
		localEphemeral: kp,
		peerPubKey:     init.EphemeralPub,
	}
	ack, err := packet.EncodeRekeyAck(packet.RekeyAckPayload{
		Epoch:        init.Epoch,
		EphemeralPub: kp.Public,
	})
	if err != nil {
		return err
	}

	oldAEAD := s.sendAEAD
	oldEpoch := s.epoch
	oldSeq, seqErr := s.allocateSendSeqLocked()
	if seqErr != nil {
		return seqErr
	}
	if err := s.completeRekey(init.Epoch, init.EphemeralPub); err != nil {
		return err
	}
	timeout := s.rekeyAckTimeout()
	go func(a []byte, aead *keys.AEADContext, epoch uint32, seq uint64) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := s.sendSealed(ctx, aead, epoch, seq, control.TypeRekeyAck, a); err != nil {
			s.mu.Lock()
			s.rekeyLastErr = err
			s.mu.Unlock()
			s.failClosed()
		}
	}(ack, oldAEAD, oldEpoch, oldSeq)
	return nil
}

func (s *Session) handleRekeyAckLocked(payload []byte) error {
	ack, err := packet.DecodeRekeyAck(payload)
	if err != nil {
		return err
	}
	if s.rekeyPending == nil || !s.rekeyPending.initiated {
		return fmt.Errorf("unexpected rekey ack")
	}
	if ack.Epoch != s.rekeyPending.newEpoch {
		return fmt.Errorf("rekey ack epoch mismatch")
	}
	s.cancelRekeyWatchdogLocked()
	return s.completeRekey(ack.Epoch, ack.EphemeralPub)
}

func (s *Session) rekeyPendingInitiated() bool {
	return s.rekeyPending != nil && s.rekeyPending.initiated
}

func (s *Session) completeRekey(epoch uint32, peerPub [32]byte) error {
	if s.rekeyPending == nil || s.rekeyPending.localEphemeral == nil {
		return fmt.Errorf("rekey state missing")
	}
	shared, err := keys.SharedSecret(&s.rekeyPending.localEphemeral.Private, &peerPub)
	if err != nil {
		return err
	}
	rekeyTranscript := append(append([]byte{}, s.transcript...), byte(epoch>>24), byte(epoch>>16), byte(epoch>>8), byte(epoch))
	sk, err := keys.DeriveSessionKeys(shared, rekeyTranscript, epoch)
	if err != nil {
		return err
	}
	// Wipe previous shared secret before installing the new one.
	for i := range s.sharedSecret {
		s.sharedSecret[i] = 0
	}
	if err := s.applyKeysLocked(sk); err != nil {
		return err
	}
	s.sharedSecret = shared
	s.rekeyPending.localEphemeral.ZeroPrivate()
	s.rekeyPending = nil
	s.rekeyAttempts = 0
	if s.state == StateRekeying {
		return s.transition(StateEstablished)
	}
	return nil
}

// RekeyLastError returns the last rekey lifecycle error (timeout/send failure), if any.
func (s *Session) RekeyLastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rekeyLastErr
}
