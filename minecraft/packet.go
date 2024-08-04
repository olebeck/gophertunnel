package minecraft

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// packetData holds the data of a Minecraft packet.
type packetData struct {
	h       *packet.Header
	full    []byte
	payload *bytes.Buffer
}

// ParseData parses the packet data slice passed into a packetData struct.
func ParseData(data []byte, PacketFunc func(header packet.Header, payload []byte)) (*packetData, error) {
	buf := bytes.NewBuffer(data)
	header := &packet.Header{}
	if err := header.Read(buf); err != nil {
		// We don't return this as an error as it's not in the hand of the user to control this. Instead,
		// we return to reading a new packet.
		return nil, fmt.Errorf("read packet header: %w", err)
	}
	if PacketFunc != nil {
		// The packet func was set, so we call it.
		PacketFunc(*header, buf.Bytes())
	}
	return &packetData{h: header, full: data, payload: buf}, nil
}

type unknownPacketError struct {
	id uint32
}

func (err unknownPacketError) Error() string {
	return fmt.Sprintf("unexpected packet (ID=%v)", err.id)
}

func (p *packetData) decode(conn *Conn) (pks []packet.Packet, err error) {
	return p.Decode(conn.pool, conn.proto, conn.Close, conn.disconnectOnUnknownPacket, conn.disconnectOnInvalidPacket, conn.shieldID.Load())
}

// decode decodes the packet payload held in the packetData and returns the packet.Packet decoded.
func (p *packetData) Decode(pool packet.Pool, proto Protocol, close func() error, DisconnectOnUnknownPacket, DisconnectOnInvalidPacket bool, ShieldID int32) (pks []packet.Packet, err error) {
	defer func() {
		if recoveredErr := recover(); recoveredErr != nil {
			err = fmt.Errorf("decode packet %v: %w", p.h.PacketID, recoveredErr.(error))
		}
		if err == nil {
			return
		}
		if ok := errors.As(err, &unknownPacketError{}); ok && DisconnectOnUnknownPacket {
			_ = close()
		}
	}()

	// Attempt to fetch the packet with the right packet ID from the pool.
	pkFunc, ok := pool[p.h.PacketID]
	var pk packet.Packet
	if !ok {
		// No packet with the ID. This may be a custom packet of some sorts.
		pk = &packet.Unknown{PacketID: p.h.PacketID}
		if DisconnectOnUnknownPacket {
			return nil, unknownPacketError{id: p.h.PacketID}
		}
	} else {
		pk = pkFunc()
	}

	r := proto.NewReader(p.payload, ShieldID, false)
	pk.Marshal(r)
	if p.payload.Len() != 0 {
		err = fmt.Errorf("decode packet %T: %v unread bytes left: 0x%x", pk, p.payload.Len(), p.payload.Bytes())
	}
	if DisconnectOnInvalidPacket && err != nil {
		return nil, err
	}
	return proto.ConvertToLatest(pk, nil), err
}
