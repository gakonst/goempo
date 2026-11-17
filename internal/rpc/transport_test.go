package rpc_test

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/kapetan-io/tiger/assert"
)

const (
	readMax    = 64
	readBuffer = 4096
)

const _ = uint(readBuffer - readMax)

func readUntil(c io.Conn, marker string) string {
	var got []byte
	var buf [readBuffer]byte
	for range readMax {
		if bytes.Contains(got, []byte(marker)) {
			break
		}
		n, err := c.Read(buf[:])
		assert.NoError(err, "read from server")
		got = append(got, buf[:n]...)
	}
	return string(got)
}

func withServer(tr *rpc.Transport, client func(c io.Conn)) {
	p := io.Pipe()
	var wg sync.WaitGroup
	wg.Go(func() { tr.ServeConn(p.B) })
	client(p.A)
	assert.NoError(p.A.Close(), "close client")
	wg.Wait()
}

func TestHTTPPost(_ *testing.T) {
	srv := rpc.NewServer(rpc.Config{}, loadChain(), nil, nil)
	withServer(rpc.NewTransport(srv), checkHTTP)
}

func checkHTTP(c io.Conn) {
	body := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_chainId\",\"params\":[]}"
	req := "POST / HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	_, err := c.Write([]byte(req))
	assert.NoError(err, "write request")
	got := readUntil(c, "}")
	assert.Ok(strings.HasPrefix(got, "HTTP/1.1 200 OK\r\n"), "status line")
	assert.Ok(strings.HasSuffix(got, "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\"0x1079\"}"), "chain id body")

	_, err = c.Write([]byte("POST / HTTP/1.1\r\nContent-Length: 3\r\n\r\n{x}"))
	assert.NoError(err, "write bad request")
	got = readUntil(c, "parse error\"}}")
	assert.Ok(strings.Contains(got, "HTTP/1.1 400 Bad Request"), "bad request status")
}

func maskedFrame(payload []byte) []byte {
	frame := []byte{0x81}
	if len(payload) < 126 {
		frame = append(frame, 0x80|byte(len(payload)))
	} else {
		frame = append(frame, 0x80|126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	}
	mask := []byte{1, 2, 3, 4}
	frame = append(frame, mask...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	return frame
}

func TestWebSocketSubscribe(_ *testing.T) {
	chain := loadChain()
	srv := rpc.NewServer(rpc.Config{}, chain, nil, nil)
	tr := rpc.NewTransport(srv)
	withServer(tr, func(c io.Conn) { checkWebSocket(c, chain, tr) })
}

func checkWebSocket(c io.Conn, chain *rpc.MemChain, tr *rpc.Transport) {
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	_, err := c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"))
	assert.NoError(err, "write upgrade")
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	got := readUntil(c, "\r\n\r\n")
	assert.Ok(strings.Contains(got, "101 Switching Protocols"), "upgrade accepted")
	assert.Ok(strings.Contains(got, base64.StdEncoding.EncodeToString(sum[:])), "accept key")

	_, err = c.Write(maskedFrame([]byte("{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"eth_chainId\"}")))
	assert.NoError(err, "write call")
	assert.Ok(strings.Contains(readUntil(c, "}"), "{\"jsonrpc\":\"2.0\",\"id\":7,\"result\":\"0x1079\"}"), "ws call answered")

	_, err = c.Write(maskedFrame([]byte("{\"jsonrpc\":\"2.0\",\"id\":8,\"method\":\"eth_subscribe\",\"params\":[\"newHeads\"]}")))
	assert.NoError(err, "write subscribe")
	assert.Ok(strings.Contains(readUntil(c, "}"), "\"id\":8,\"result\":\"0x1\""), "subscription id")

	head, ok := chain.Block(chain.Head())
	assert.Ok(ok, "head block")
	assert.NoError(tr.Publish(head), "publish head")
	note := readUntil(c, "timestampMillis")
	assert.Ok(strings.Contains(note, "\"method\":\"eth_subscription\""), "notification")
	assert.Ok(strings.Contains(note, head.Hash.Hex()), "notification carries the head hash")
}
