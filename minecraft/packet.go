package minecraft

import (
	"bytes"
	"fmt"
	"net"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// packetData holds the data of a Minecraft packet.
type packetData struct {
	h       *packet.Header
	full    []byte
	payload *bytes.Buffer
}

// parseData parses the packet data slice passed into a packetData struct.
func parseData(data []byte, conn IConn, src, dst net.Addr) (*packetData, error) {
	buf := bytes.NewBuffer(data)
	header := &packet.Header{}
	if err := header.Read(buf); err != nil {
		// We don't return this as an error as it's not in the hand of the user to control this. Instead,
		// we return to reading a new packet.
		return nil, fmt.Errorf("error reading packet header: %v", err)
	}
	// The packet func was set, so we call it.
	conn.PacketFunc(*header, buf.Bytes(), src, dst)
	return &packetData{h: header, full: data, payload: buf}, nil
}

func ParseData(data []byte, conn IConn, src, dst net.Addr) (*packetData, error) {
	return parseData(data, conn, src, dst)
}

type unknownPacketError struct {
	id uint32
}

func (err unknownPacketError) Error() string {
	return fmt.Sprintf("unknown packet with ID %v", err.id)
}

func (p *packetData) Decode(conn IConn) (pks []packet.Packet, err error) {
	return p.decode(conn)
}

// decode decodes the packet payload held in the packetData and returns the packet.Packet decoded.
func (p *packetData) decode(conn IConn) (pks []packet.Packet, err error) {
	defer func() {
		if recoveredErr := recover(); recoveredErr != nil {
			err = fmt.Errorf("packet %v: %w", p.h.PacketID, recoveredErr.(error))
		}
		if err == nil {
			return
		}
		if _, ok := err.(unknownPacketError); ok {
			if conn.DisconnectOnUnknownPacket() {
				_ = conn.Close()
			}
		} else if conn.DisconnectOnInvalidPacket() {
			_ = conn.Close()
		}
	}()

	// Attempt to fetch the packet with the right packet ID from the pool.
	pkFunc, ok := conn.Pool()[p.h.PacketID]
	var pk packet.Packet
	if !ok {
		// No packet with the ID. This may be a custom packet of some sorts.
		pk = &packet.Unknown{PacketID: p.h.PacketID}
		if conn.DisconnectOnUnknownPacket() {
			return nil, unknownPacketError{id: p.h.PacketID}
		}
	} else {
		pk = pkFunc()
	}

	r := conn.Proto().NewReader(p.payload, conn.ShieldID(), false)
	pk.Marshal(r)
	if p.payload.Len() != 0 {
		err = fmt.Errorf("%T: %v unread bytes left: 0x%x", pk, p.payload.Len(), p.payload.Bytes())
	}
	if conn.DisconnectOnInvalidPacket() && err != nil {
		return nil, err
	}
	return conn.Proto().ConvertToLatest(pk, conn), err
}
