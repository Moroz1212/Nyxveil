package controlplane

import (
	"errors"
	"fmt"
)

// AcceptedLocalError means Control Plane returned HTTP 2xx but the client failed
// while decoding or applying the response body. The server-side operation may
// already have committed (e.g. node registration + bootstrap consumption).
type AcceptedLocalError struct {
	Method string
	Path   string
	Status int
	Body   []byte
	Err    error
}

func (e *AcceptedLocalError) Error() string {
	if e == nil {
		return "controlplane: accepted local error"
	}
	return fmt.Sprintf("controlplane: HTTP %d %s %s accepted but local processing failed: %v",
		e.Status, e.Method, e.Path, e.Err)
}

func (e *AcceptedLocalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsAcceptedLocal reports whether err (or a wrap) is an AcceptedLocalError.
func IsAcceptedLocal(err error) bool {
	var ae *AcceptedLocalError
	return errors.As(err, &ae)
}
