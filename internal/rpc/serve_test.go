package rpc_test

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/kapetan-io/tiger/assert"
	"go.uber.org/goleak"
)

const concurrentClients = 12

func httpCall(c io.Conn, body string) string {
	req := "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	_, err := c.Write([]byte(req))
	assert.NoError(err, "write request")
	return readUntil(c, "}")
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestServeHandlesConnectionsConcurrently(t *testing.T) {
	defer goleak.VerifyNone(t)
	network := io.NewMemNetwork()
	l, err := network.Listen("rpc")
	assert.NoError(err, "listen")
	tr := rpc.NewTransport(rpc.NewServer(rpc.Config{}, loadChain(), nil, nil))
	var served sync.WaitGroup
	served.Go(func() { assert.NoError(tr.Serve(l), "serve returns cleanly on close") })

	idle, err := network.Dial("rpc")
	assert.NoError(err, "dial idle client")
	_, err = idle.Write([]byte("POST / HTTP/1.1\r\n"))
	assert.NoError(err, "half a request keeps the first connection busy")

	var clients sync.WaitGroup
	for i := range concurrentClients {
		clients.Go(func() {
			c, err := network.Dial("rpc")
			assert.NoError(err, "dial")
			body := `{"jsonrpc":"2.0","id":` + strconv.Itoa(i) + `,"method":"eth_chainId","params":[]}`
			got := httpCall(c, body)
			assert.Ok(strings.HasSuffix(got, `"result":"0x1079"}`), "answered while another connection is open")
			assert.NoError(c.Close(), "close client")
		})
	}
	clients.Wait()
	assert.NoError(tr.Close(), "close transport")
	served.Wait()
	assert.NoError(idle.Close(), "close idle client")
	assert.Ok(tr.Serve(l) != nil, "a closed transport refuses to serve again")
}
