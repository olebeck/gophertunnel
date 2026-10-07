package minecraft

import (
	"context"
	"testing"
	"time"

	"github.com/sandertv/go-raknet"
)

// PlayerCount counts local transports, including incomplete logins, not players
// on an upstream Realm. Early rejection must release each such count.
func TestEarlyRejectedConnectionsReleasePlayerCount(t *testing.T) {
	var listener *Listener
	ready := make(chan struct{})
	rejected := make(chan error, 3)
	var err error
	listener, err = (ListenConfig{
		AuthenticationDisabled: true,
		EarlyConnHandler:       func(c *Conn) { <-ready; rejected <- listener.Disconnect(c, "Already connected to recorder") },
	}).Listen("raknet", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	close(ready)
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		c, err := (raknet.Dialer{}).DialContext(ctx, listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		select {
		case err := <-rejected:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("early rejection blocked")
		}
	}
	for listener.PlayerCount() != 0 {
		select {
		case <-ctx.Done():
			t.Fatalf("rejected local transports still counted: %d", listener.PlayerCount())
		case <-time.After(time.Millisecond):
		}
	}
}
