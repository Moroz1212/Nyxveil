package server

import (
	"context"
	"sync"

	"github.com/nyxveil/nvp/auth/ticket"
	"github.com/nyxveil/nvp/control"
	"github.com/nyxveil/nvp/session"
)

// AuthHandler validates access tickets on VPN nodes.
type AuthHandler struct {
	Verifier   ticket.VerifierConfig
	NodeID     string
	MaxPending int
	pending    int
	mu         sync.Mutex
}

// NewAuthHandler creates node-side auth handler.
func NewAuthHandler(nodeID string, verifier ticket.VerifierConfig) *AuthHandler {
	return &AuthHandler{
		Verifier:   verifier,
		NodeID:     nodeID,
		MaxPending: 256,
	}
}

// HandleAuth validates ticket from AUTH message payload.
func (h *AuthHandler) HandleAuth(ctx context.Context, sess *session.Session, ticketBytes []byte) error {
	h.mu.Lock()
	if h.pending >= h.MaxPending {
		h.mu.Unlock()
		return sess.HandleAuthFail(ctx, control.AuthFailRateLimited)
	}
	h.pending++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.pending--
		h.mu.Unlock()
	}()

	tokenStr := string(ticketBytes)
	claims, err := ticket.Verify(h.Verifier, tokenStr, "", h.NodeID)
	if err != nil {
		reason := control.AuthFailInvalidTicket
		switch err {
		case ticket.ErrExpired:
			reason = control.AuthFailExpired
		case ticket.ErrWrongDevice:
			reason = control.AuthFailWrongDevice
		case ticket.ErrRevoked:
			reason = control.AuthFailRevoked
		case ticket.ErrWrongScope:
			reason = control.AuthFailWrongScope
		}
		_ = sess.HandleAuthFail(ctx, reason)
		return err
	}

	_ = claims
	if err := sess.MarkEstablished(); err != nil {
		return err
	}
	return sess.HandleAuthOK(ctx)
}
