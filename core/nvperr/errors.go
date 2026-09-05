// Package nvperr exports stable, errors.Is-friendly sentinel errors for the NVP client.
package nvperr

import "errors"

var (
	// ErrLicenseInvalid indicates the license token was rejected or missing.
	ErrLicenseInvalid = errors.New("license invalid")
	// ErrLicenseExpired indicates the license is past its validity window.
	ErrLicenseExpired = errors.New("license expired")
	// ErrDeviceUnauthorized indicates the device is not activated or revoked.
	ErrDeviceUnauthorized = errors.New("device unauthorized")
	// ErrTicketRejected indicates the access ticket was rejected by the node.
	ErrTicketRejected = errors.New("ticket rejected")
	// ErrLocationNotAllowed indicates the requested location is outside license scope.
	ErrLocationNotAllowed = errors.New("location not allowed")
	// ErrNoHealthyNodes indicates catalog selection found no usable nodes.
	ErrNoHealthyNodes = errors.New("no healthy nodes available")
	// ErrTransportUnavailable indicates transport dial/failover exhausted without a connection.
	ErrTransportUnavailable = errors.New("transport unavailable")
	// ErrServerIdentityMismatch indicates TLS/SPKI pin or identity verification failed.
	ErrServerIdentityMismatch = errors.New("server identity mismatch")
	// ErrECHRequiredUnavailable indicates ECH was required but could not be negotiated.
	ErrECHRequiredUnavailable = errors.New("ECH required but unavailable")
	// ErrAuthTimeout indicates AUTH_OK was not received before the auth deadline.
	ErrAuthTimeout = errors.New("authentication timeout")
	// ErrHandshakeFailed indicates the VPN handshake did not complete successfully.
	ErrHandshakeFailed = errors.New("handshake failed")
	// ErrSessionClosed indicates the session is closed.
	ErrSessionClosed = errors.New("session closed")
	// ErrAuthFailed indicates the peer rejected authentication (AUTH_FAIL).
	ErrAuthFailed = errors.New("authentication failed")
	// ErrDeviceKeyRequired indicates a device-bound ticket was presented without a device private key.
	ErrDeviceKeyRequired = errors.New("device private key required")
)
