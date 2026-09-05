package tlsstream

import (
	"encoding/binary"
	"fmt"

	"github.com/nyxveil/nvp/core/protocol"
)

// EncodeFrame builds a single length-prefixed buffer (uint32 BE || payload).
// Callers must Write the result in one shot so TLS does not split a bare length record.
func EncodeFrame(payload []byte) ([]byte, error) {
	if len(payload) == 0 || len(payload) > protocol.MaxFrameSize {
		return nil, fmt.Errorf("invalid frame length %d", len(payload))
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}
