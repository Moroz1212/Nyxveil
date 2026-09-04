package windows_test

import (
	"context"
	"runtime"
	"testing"

	win "github.com/nyxveil/nvp/client/windows"
	"github.com/nyxveil/nvp/tunnel"
)

func TestWindowsTUNFactory(t *testing.T) {
	f := win.NewFactory()
	_, err := f.Open(context.Background(), tunnel.Config{Name: "Nyxveil", MTU: 1280})
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatal(err)
		}
	} else if err == nil {
		t.Fatal("expected error on non-windows")
	}
}
