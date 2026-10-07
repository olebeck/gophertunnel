package protocol

import (
	"bytes"
	"testing"
)

func TestCurrentProtocol12650(t *testing.T) {
	if CurrentProtocol != 2193 || CurrentVersion != "1.26.50" {
		t.Fatalf("expected real 1.26.50 codecs/protocol 2193, got %s/%d", CurrentVersion, CurrentProtocol)
	}
}

func Test12650InputFlagsWireHasNoPresencePrefix(t *testing.T) {
	flags := NewInputFlagsFromIDs(16, []int32{1, 7})
	var b bytes.Buffer
	InputFlagList(NewWriter(&b, 0), &flags, 16)
	want := []byte{2, 2, 14} // count (varuint32), IDs (varint32).
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("wire flags: got %x, want %x", b.Bytes(), want)
	}
	var decoded InputFlags
	InputFlagList(NewReader(bytes.NewBuffer(want), 0, true), &decoded, 16)
	if !decoded.Load(1) || !decoded.Load(7) || decoded.Load(0) {
		t.Fatalf("wrong decoded flags: %+v", decoded)
	}
}
