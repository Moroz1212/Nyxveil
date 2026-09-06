package authhandler

import (
	"context"
	"errors"
	"sync"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/session"
)

// ErrMissingConnectPermission is returned when a ticket lacks PermissionConnect.
var ErrMissingConnectPermission = errors.New("ticket missing connect permission")

// AuthHandler validates access tickets on VPN nodes.
type AuthHandler struct {
	Verifier   ticket.VerifierConfig
	NodeID     string
	LocationID string
	MaxPending int
	pending    int
	mu         sync.Mutex
}

// NewAuthHandler creates node-side auth handler.
func NewAuthHandler(nodeID, locationID string, verifier ticket.VerifierConfig) *AuthHandler {
	return &AuthHandler{
		Verifier:   verifier,
		NodeID:     nodeID,
		LocationID: locationID,
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

	jwtStr, sig, err := ticket.SplitAuthPayload(ticketBytes)
	if err != nil {
		_ = sess.HandleAuthFail(ctx, control.AuthFailInvalidTicket)
		return err
	}

	claims, err := ticket.VerifyAt(h.Verifier, jwtStr, "", h.NodeID, h.LocationID)
	if err != nil {
		_ = sess.HandleAuthFail(ctx, control.AuthFailInvalidTicket)
		return err
	}
	if !claims.HasPermission(ticket.PermissionConnect) {
		_ = sess.HandleAuthFail(ctx, control.AuthFailInvalidTicket)
		return ErrMissingConnectPermission
	}
	if err := ticket.VerifySessionBinding(claims, jwtStr, sig, sess.Transcript()); err != nil {
		_ = sess.HandleAuthFail(ctx, control.AuthFailInvalidTicket)
		return err
	}

	if err := sess.MarkEstablished(); err != nil {
		return err
	}
	return sess.HandleAuthOK(ctx)
}
