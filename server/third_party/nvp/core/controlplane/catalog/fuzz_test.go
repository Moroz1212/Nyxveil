package catalog_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
)

func FuzzParseCatalog(f *testing.F) {
	seed, _ := json.Marshal(model.SignedCatalog{
		Catalog: model.Catalog{
			Version:   "1",
			IssuedAt:  time.Now().UTC(),
			ExpiresAt: time.Now().Add(time.Hour).UTC(),
		},
		KeyID:     "k1",
		Signature: []byte{1, 2, 3},
	})
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{`))
	f.Add([]byte(nil))
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		signed, err := catalog.Parse(data)
		if err != nil {
			return
		}
		_ = catalog.Verify(catalog.VerifyKeys{Keys: nil}, signed)
	})
}
