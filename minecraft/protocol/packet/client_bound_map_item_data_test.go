package packet

import (
	"bytes"
	"image/color"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Map pixels are sent as the bytes R, G, B, A. Reading them as big endian
// ARGB swapped red and blue (a yellow map came out blue).
func TestClientBoundMapItemDataPixelOrder(t *testing.T) {
	yellow := color.RGBA{R: 0xff, G: 0xd8, B: 0x10, A: 0xff}
	pk := &ClientBoundMapItemData{
		MapID:   1,
		Width:   protocol.Option(int32(1)),
		Height:  protocol.Option(int32(1)),
		XOffset: protocol.Option(int32(0)),
		YOffset: protocol.Option(int32(0)),
		Pixels:  protocol.Option([]color.RGBA{yellow}),
	}
	buf := new(bytes.Buffer)
	pk.Marshal(protocol.NewWriter(buf, 0))
	raw := buf.Bytes()
	if !bytes.HasSuffix(raw, []byte{0xff, 0xd8, 0x10, 0xff}) {
		t.Fatalf("pixel not written as R, G, B, A: % x", raw)
	}

	var back ClientBoundMapItemData
	back.Marshal(protocol.NewReader(bytes.NewBuffer(raw), 0, false))
	px, _ := back.Pixels.Value()
	if len(px) != 1 || px[0] != yellow {
		t.Fatalf("read %v, want %v", px, yellow)
	}
}
