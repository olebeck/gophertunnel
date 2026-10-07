package minecraft

import (
	"crypto/ecdsa"
	"net/url"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

func TestNetherNetLoginServerAddressRepeatsURLPort(t *testing.T) {
	for _, tt := range []struct{ address, want string }{
		{"https://host.example:19132", "https://host.example:19132:19132"},
		{"https://host.example", "https://host.example"},
		{"host.example:19132", "host.example:19132"},
		{"https://[::1]:19132", "https://[::1]:19132:19132"},
		{"https://host.example:bad", "https://host.example:bad"},
	} {
		t.Run(tt.address, func(t *testing.T) {
			var d login.ClientData
			defaultClientData(tt.address, "fixture", &d)
			if d.ServerAddress != tt.want {
				t.Fatalf("incorrect NetherNet login server address: got %s, want %s", d.ServerAddress, tt.want)
			}
		})
	}
}

func TestNetherNetIdentityIncludesAuthorizationIssuer(t *testing.T) {
	for _, raw := range []string{"https://authorization.example/", "https://authorization.example"} {
		t.Run(raw, func(t *testing.T) {
			issuer, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			key := &ecdsa.PrivateKey{}
			identity := netherNetIdentity(key, "fixture-not-a-token", issuer)
			if identity.Domain != "https://authorization.example/" || identity.Token != "fixture-not-a-token" || identity.PrivateKey != key {
				t.Fatalf("incorrect identity: %+v", identity)
			}
		})
	}
}
