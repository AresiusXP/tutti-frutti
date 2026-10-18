package ws

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// rawConn is a hand-rolled WebSocket client, so a test controls every byte
// that reaches the server (partial frames, stalls).
type rawConn struct {
	t  *testing.T
	nc net.Conn
	br *bufio.Reader
}

// rawDial performs the HTTP upgrade by hand and returns once the server
// answered 101.
func (e *env) rawDial(opts ...dialOpt) *rawConn {
	e.t.Helper()
	h := http.Header{}
	h.Set("Cookie", (&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value}).String())
	h.Set("Origin", testOrigin)
	for _, o := range opts {
		o(h)
	}
	nc, err := net.DialTimeout("tcp", strings.TrimPrefix(e.srv.URL, "http://"), waitLimit)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = nc.Close() })

	var req bytes.Buffer
	fmt.Fprintf(&req, "GET / HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n", strings.TrimPrefix(e.srv.URL, "http://"))
	_ = h.Write(&req)
	req.WriteString("\r\n")
	if _, err := nc.Write(req.Bytes()); err != nil {
		e.t.Fatal(err)
	}
	rc := &rawConn{t: e.t, nc: nc, br: bufio.NewReader(nc)}
	_ = nc.SetReadDeadline(time.Now().Add(waitLimit))
	resp, err := http.ReadResponse(rc.br, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		e.t.Fatalf("raw upgrade = %d", resp.StatusCode)
	}
	return rc
}

func (rc *rawConn) write(b []byte) {
	rc.t.Helper()
	_ = rc.nc.SetWriteDeadline(time.Now().Add(waitLimit))
	if _, err := rc.nc.Write(b); err != nil {
		rc.t.Fatalf("raw write: %v", err)
	}
}

// clientFrameHeader encodes the header of a masked client frame that declares
// a payload of declared bytes (mask key included).
func clientFrameHeader(opcode byte, declared int) []byte {
	b := []byte{0x80 | opcode}
	switch {
	case declared < 126:
		b = append(b, 0x80|byte(declared))
	case declared < 1<<16:
		b = append(b, 0x80|126)
		b = binary.BigEndian.AppendUint16(b, uint16(declared))
	default:
		b = append(b, 0x80|127)
		b = binary.BigEndian.AppendUint64(b, uint64(declared))
	}
	return append(b, 1, 2, 3, 4)
}

func maskPayload(p []byte) []byte {
	key := []byte{1, 2, 3, 4}
	out := make([]byte, len(p))
	for i := range p {
		out[i] = p[i] ^ key[i%4]
	}
	return out
}

const opText, opClose = 0x1, 0x8

// textFrame is a complete masked client text frame.
func textFrame(s string) []byte {
	return append(clientFrameHeader(opText, len(s)), maskPayload([]byte(s))...)
}

// partialFrame is the header of a text frame declaring declared bytes followed
// by only the first sent payload bytes.
func partialFrame(declared, sent int) []byte {
	return append(clientFrameHeader(opText, declared), maskPayload(bytes.Repeat([]byte("x"), sent))...)
}

// readFrame reads one (unmasked) server frame.
func (rc *rawConn) readFrame(timeout time.Duration) (opcode byte, payload []byte, err error) {
	_ = rc.nc.SetReadDeadline(time.Now().Add(timeout))
	var hdr [2]byte
	if _, err = io.ReadFull(rc.br, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := int(hdr[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(rc.br, ext[:]); err != nil {
			return 0, nil, err
		}
		n = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(rc.br, ext[:]); err != nil {
			return 0, nil, err
		}
		n = int(binary.BigEndian.Uint64(ext[:]))
	}
	payload = make([]byte, n)
	_, err = io.ReadFull(rc.br, payload)
	return hdr[0] & 0x0f, payload, err
}

// untilClosed reads server frames until the connection ends. It returns the
// close code and reason of the close frame (0 if none arrived), whether the
// server then dropped the TCP connection (EOF), and how long it all took.
func (rc *rawConn) untilClosed(timeout time.Duration) (code int, reason string, eof bool) {
	rc.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return code, reason, false
		}
		op, payload, err := rc.readFrame(left)
		if err != nil {
			return code, reason, err == io.EOF || strings.Contains(err.Error(), "reset") || err == io.ErrUnexpectedEOF
		}
		if op == opClose && len(payload) >= 2 {
			code = int(binary.BigEndian.Uint16(payload))
			reason = string(payload[2:])
		}
	}
}
