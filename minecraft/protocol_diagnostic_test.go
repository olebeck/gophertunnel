package minecraft

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestRejectedClientProtocolIsLoggedBeforeLogin(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	var b bytes.Buffer
	c := newConn(left, nil, slog.New(slog.NewTextHandler(&b, nil)), DefaultProtocol, 0, true)
	c.acceptedProto = []Protocol{DefaultProtocol}
	err := c.handleRequestNetworkSettings(&packet.RequestNetworkSettings{ClientProtocol: 9999})
	if err == nil || !strings.Contains(err.Error(), "got 9999") {
		t.Fatalf("expected numeric protocol rejection, got %v", err)
	}
	if text := b.String(); !strings.Contains(text, "client_protocol=9999") || !strings.Contains(text, "supported_protocol=2193") {
		t.Fatalf("missing nonsecret pre-login protocol diagnostic: %q", text)
	}
}
