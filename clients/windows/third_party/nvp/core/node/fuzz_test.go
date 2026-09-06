package node_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/node"
)

func FuzzParseNodeDescriptor(f *testing.F) {
	seed, _ := json.Marshal(node.SignedDescriptor{
		Descriptor: node.Descriptor{
			NodeID:     "fi-hel-01",
			LocationID: "fi-hel",
			Status:     node.StatusHealthy,
			Enabled:    true,
			UpdatedAt:  time.Now().UTC(),
		},
		KeyID:     "k1",
		Signature: []byte{1, 2, 3},
	})
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`[`))
	f.Add([]byte(nil))
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		_, _ = node.ParseSignedDescriptor(data)
		_, _ = node.ParseDescriptor(data)
	})
}
