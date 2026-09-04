package session

import (
	"context"
	"fmt"

	"github.com/nyxveil/nvp/control"
	"github.com/nyxveil/nvp/keys"
	"github.com/nyxveil/nvp/packet"
)

type rekeyState struct {
	newEpoch       uint32
	localEphemeral *keys.EphemeralKeypair
	peerPubKey     [32]byte
	initiated      bool
}

func (s *Session) prepareRekeyLocked() ([]byte, error) {
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
	return packet.EncodeRekeyInit(packet.RekeyInitPayload{
		Epoch:        newEpoch,
		EphemeralPub: kp.Public,
	})
}

func (s *Session) sendRekeyControl(ctx context.Context, msgType byte, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendAEAD == nil {
		return ErrAuthRequired
	}
	inner, err := packet.EncodeInner(msgType, payload, nil)
	if err != nil {
		return err
	}
	seq := s.sendSeq
	s.sendSeq++
	ct, err := s.sendAEAD.Seal(s.epoch, seq, inner)
	if err != nil {
		return err
	}
	wire, err := packet.EncodeWireRecord(ct)
	if err != nil {
		return err
	}
	return s.conn.Write(ctx, wire)
}

func (s *Session) handleRekeyLocked(payload []byte) error {
	if s.state != StateEstablished && s.state != StateRekeying {
		return fmt.Errorf("%w: rekey in %s", ErrInvalidState, s.state)
	}

	init, err := packet.DecodeRekeyInit(payload)
	if err != nil {
		return err
	}

	if !s.rekeyPendingInitiated() {
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
		if err := s.completeRekey(init.Epoch, init.EphemeralPub); err != nil {
			return err
		}
		go func(a []byte) {
			_ = s.sendRekeyControl(context.Background(), control.TypeRekeyAck, a)
		}(ack)
		return nil
	}
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
	rekeyTranscript := append(append([]byte{}, s.transcript...), byte(epoch))
	sk, err := keys.DeriveSessionKeys(shared, rekeyTranscript, epoch)
	if err != nil {
		return err
	}
	s.applyKeysLocked(sk)
	s.sharedSecret = shared
	s.rekeyPending = nil
	if s.state == StateRekeying {
		return s.transition(StateEstablished)
	}
	return nil
}
