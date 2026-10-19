package main

import (
	"fmt"
	"net"
	"sync"
	"time"
)

type streamEvent struct {
	connected       bool
	disconnected    bool
	accessUnit      []byte
	keyframe        bool
	alertState      *bool
	rotation        *int
	notice          string
	broadcastResult *broadcastResult
}

type broadcastResult struct {
	started bool
	reason  string
}

// A usbmux forward accepts connections even when nothing listens on the device.
type streamClient struct {
	name    string
	port    uint16
	handler func(streamEvent)

	mu   sync.Mutex
	conn net.Conn
}

func newStreamClient(name string, port uint16, handler func(streamEvent)) *streamClient {
	return &streamClient{name: name, port: port, handler: handler}
}

func (c *streamClient) start() {
	go func() {
		for {
			c.runOnce()
			time.Sleep(time.Second)
		}
	}()
}

func (c *streamClient) send(data []byte) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		_, _ = conn.Write(data)
	}
}

func (c *streamClient) runOnce() {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", c.port), 2*time.Second)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		_ = conn.Close()
	}()

	receiving := false
	reader := packetReader{}
	buffer := make([]byte, 256*1024)
	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			if !receiving {
				receiving = true
				logf("%s connected", c.name)
				c.handler(streamEvent{connected: true})
			}
			for _, p := range reader.append(buffer[:n]) {
				switch {
				case p.kind == packetAccessUnit && len(p.payload) > 0:
					c.handler(streamEvent{accessUnit: p.payload[1:], keyframe: p.payload[0]&1 == 1})
				case p.kind == packetHello && string(p.payload) != protocolVersion:
					logf("the installed runner speaks protocol %s, this ios-screencast speaks %s. Install the matching IPAs", p.payload, protocolVersion)
				case p.kind == packetBroadcastResult && len(p.payload) > 0:
					c.handler(streamEvent{broadcastResult: &broadcastResult{started: p.payload[0] == 1, reason: string(p.payload[1:])}})
				case p.kind == packetNotice:
					c.handler(streamEvent{notice: string(p.payload)})
				case p.kind == packetRotation && len(p.payload) > 0:
					quarterTurns := int(p.payload[0] % 4)
					c.handler(streamEvent{rotation: &quarterTurns})
				case p.kind == packetAlertState && len(p.payload) > 0:
					visible := p.payload[0] == 1
					c.handler(streamEvent{alertState: &visible})
				}
			}
		}
		if err != nil {
			if receiving {
				logf("%s disconnected", c.name)
				c.handler(streamEvent{disconnected: true})
			}
			return
		}
	}
}
