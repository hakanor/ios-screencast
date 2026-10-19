package main

import (
	"encoding/binary"
	"encoding/json"
)

const (
	packetAccessUnit      = 1
	packetAlertState      = 2
	packetSettings        = 3
	packetCommand         = 4
	packetHello           = 5
	packetRotation        = 6
	packetNotice          = 7
	packetBroadcastResult = 8

	protocolVersion = "1"
)

type Mode string

const (
	ModeMix       Mode = "mix"
	ModeRunner    Mode = "runner"
	ModeBroadcast Mode = "broadcast"
)

type Settings struct {
	Mode    Mode    `json:"mode"`
	Scale   float64 `json:"scale"`
	FPS     int     `json:"fps"`
	Bitrate int     `json:"bitrate"`
}

var defaultSettings = Settings{Mode: ModeMix, Scale: 1.0, FPS: 60, Bitrate: 12_000_000}

func encodePacket(packetType byte, payload []byte) []byte {
	data := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(data, uint32(len(payload)+1))
	data[4] = packetType
	copy(data[5:], payload)
	return data
}

func settingsPacket(s Settings) []byte {
	payload, _ := json.Marshal(s)
	return encodePacket(packetSettings, payload)
}

func commandPacket(name string) []byte {
	return encodePacket(packetCommand, []byte(name))
}

type packetReader struct {
	buffer []byte
}

type packet struct {
	kind    byte
	payload []byte
}

func (r *packetReader) append(data []byte) []packet {
	r.buffer = append(r.buffer, data...)
	var packets []packet
	for len(r.buffer) >= 5 {
		length := int(binary.BigEndian.Uint32(r.buffer))
		if length < 1 || len(r.buffer) < 4+length {
			break
		}
		payload := make([]byte, length-1)
		copy(payload, r.buffer[5:4+length])
		packets = append(packets, packet{kind: r.buffer[4], payload: payload})
		r.buffer = r.buffer[4+length:]
	}
	return packets
}
