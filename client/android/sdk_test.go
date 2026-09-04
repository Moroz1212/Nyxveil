package android_test

import (
	"context"
	"testing"

	"github.com/nyxveil/nvp/client/android"
	"github.com/nyxveil/nvp/tunnel"
)

func TestSDKConnectRequiresCredentials(t *testing.T) {
	sdk := android.NewSDK()
	err := sdk.Connect(context.Background(), android.ConnectConfig{})
	if err == nil {
		t.Fatal("expected error without credentials")
	}
}

func TestSDKRouteMode(t *testing.T) {
	sdk := android.NewSDK()
	sdk.SetRouteMode(tunnel.RouteSelectedApps)
	if sdk.RouteMode() != tunnel.RouteSelectedApps {
		t.Fatal("route mode not set")
	}
}

func TestTUNDevice(t *testing.T) {
	d := android.NewTUNDevice("tun0", 1280,
		func(p []byte) (int, error) { return copy(p, []byte{1, 2}), nil },
		func(p []byte) (int, error) { return len(p), nil },
		func() error { return nil },
	)
	n, err := d.Read(make([]byte, 4))
	if err != nil || n != 2 {
		t.Fatalf("read: %d %v", n, err)
	}
}
