package rpc

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/gakonst/goempo/internal/io"
)

const (
	headerLineMax      = 8 << 10
	headerLinesMax     = 128
	bodyMax            = 16 << 20
	readChunk          = 32 << 10
	requestsPerConnMax = 1 << 20
	wsMessagesMax      = 1 << 30
	wsFragmentsMax     = 1024
	wsControlMax       = 125
	subscriptionsMax   = 1024
	wsGUID             = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

const (
	_ = uint(bodyMax - headerLineMax)
	_ = uint(headerLineMax - headerLinesMax)
	_ = uint(wsMessagesMax - wsFragmentsMax)
	_ = uint(wsFragmentsMax - wsControlMax)
	_ = uint(subscriptionsMax - wsControlMax)
	_ = uint(readChunk - wsControlMax)
	_ = uint(requestsPerConnMax - headerLinesMax)
)

var (
	ErrHeaderTooLarge = errors.New("rpc: http header too large")
	ErrBodyTooLarge   = errors.New("rpc: request body too large")
	ErrBadRequest     = errors.New("rpc: malformed http request")
	ErrFrameTooLarge  = errors.New("rpc: websocket frame too large")
	ErrBadFrame       = errors.New("rpc: malformed websocket frame")
)

type Transport struct {
	srv       *Server
	listener  io.Listener
	wg        sync.WaitGroup
	stopOnce  sync.Once
	mu        sync.Mutex
	sockets   map[uint64]*socket
	socketSeq uint64
	subSeq    uint64
	conns     map[uint64]io.Conn
	connSeq   uint64
	closed    bool
	done      chan struct{}
}

func NewTransport(srv *Server) *Transport {
	return &Transport{srv: srv, sockets: map[uint64]*socket{}, conns: map[uint64]io.Conn{}, done: make(chan struct{})}
}

func (t *Transport) Shutdown() error {
	return t.closeSockets()
}

func (t *Transport) openSockets() []*socket {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*socket, 0, len(t.sockets))
	for _, id := range slices.Sorted(maps.Keys(t.sockets)) {
		out = append(out, t.sockets[id])
	}
	return out
}

func (t *Transport) closeSockets() error {
	var err error
	for _, sk := range t.openSockets() {
		err = errors.Join(err, sk.conn.Close())
	}
	return err
}

type connReader struct {
	conn io.Conn
	buf  []byte
	pos  int
}

func (r *connReader) fill() error {
	if r.pos > 0 {
		r.buf = append(r.buf[:0], r.buf[r.pos:]...)
		r.pos = 0
	}
	var chunk [readChunk]byte
	n, err := r.conn.Read(chunk[:])
	r.buf = append(r.buf, chunk[:n]...)
	if n > 0 {
		return nil
	}
	if err == nil {
		return io.ErrConnClosed
	}
	return err
}

func (r *connReader) line() (string, error) {
	for range headerLineMax / wsControlMax {
		if i := bytes.Index(r.buf[r.pos:], []byte("\r\n")); i >= 0 {
			s := string(r.buf[r.pos : r.pos+i])
			r.pos += i + 2
			return s, nil
		}
		if len(r.buf)-r.pos > headerLineMax {
			return "", ErrHeaderTooLarge
		}
		if err := r.fill(); err != nil {
			return "", err
		}
	}
	return "", ErrHeaderTooLarge
}

func (r *connReader) take(n int) ([]byte, error) {
	if n > bodyMax {
		return nil, ErrBodyTooLarge
	}
	for range n/wsControlMax + 2 {
		if len(r.buf)-r.pos >= n {
			out := append([]byte(nil), r.buf[r.pos:r.pos+n]...)
			r.pos += n
			return out, nil
		}
		if err := r.fill(); err != nil {
			return nil, err
		}
	}
	return nil, ErrBodyTooLarge
}

type httpRequest struct {
	method  string
	headers map[string]string
	body    []byte
}

func readRequest(r *connReader) (httpRequest, error) {
	first, err := r.line()
	if err != nil {
		return httpRequest{}, err
	}
	parts := strings.Fields(first)
	if len(parts) != 3 {
		return httpRequest{}, ErrBadRequest
	}
	req := httpRequest{method: parts[0], headers: map[string]string{}}
	if err := readHeaders(r, req.headers); err != nil {
		return httpRequest{}, err
	}
	if strings.EqualFold(req.headers["transfer-encoding"], "chunked") {
		return httpRequest{}, ErrBadRequest
	}
	size, err := contentLength(req.headers["content-length"])
	if err != nil {
		return httpRequest{}, err
	}
	if req.body, err = r.take(size); err != nil {
		return httpRequest{}, err
	}
	return req, nil
}

func readHeaders(r *connReader, into map[string]string) error {
	for range headerLinesMax {
		line, err := r.line()
		if err != nil {
			return err
		}
		if line == "" {
			return nil
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return ErrBadRequest
		}
		into[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
	}
	return ErrHeaderTooLarge
}

func contentLength(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, ErrBadRequest
	}
	if n < 0 {
		return 0, ErrBadRequest
	}
	if n > bodyMax {
		return 0, ErrBodyTooLarge
	}
	return n, nil
}

func writeHTTP(conn io.Conn, status int, body []byte) error {
	var b bytes.Buffer
	b.WriteString("HTTP/1.1 " + strconv.Itoa(status) + " " + statusText(status) + "\r\n")
	b.WriteString("Content-Type: application/json\r\n")
	b.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n")
	b.Write(body)
	_, err := conn.Write(b.Bytes())
	return err
}

func statusText(status int) string {
	switch status {
	case StatusOK:
		return "OK"
	case StatusBadRequest:
		return "Bad Request"
	case statusMethodNotAllowed:
		return "Method Not Allowed"
	case statusTooLarge:
		return "Payload Too Large"
	}
	return "Error"
}

const (
	statusSwitching        = 101
	statusMethodNotAllowed = 405
	statusTooLarge         = 413
)

func isUpgrade(req *httpRequest) bool {
	if !strings.EqualFold(req.headers["upgrade"], "websocket") {
		return false
	}
	return req.headers["sec-websocket-key"] != ""
}

func (t *Transport) ServeConn(conn io.Conn) {
	defer conn.Close()
	r := &connReader{conn: conn}
	for range requestsPerConnMax {
		req, err := readRequest(r)
		if errors.Is(err, ErrBodyTooLarge) {
			if werr := writeHTTP(conn, statusTooLarge, nil); werr != nil {
				return
			}
			return
		}
		if err != nil {
			return
		}
		if isUpgrade(&req) {
			t.serveSocket(conn, r, req.headers["sec-websocket-key"])
			return
		}
		if !t.answerHTTP(conn, &req) {
			return
		}
	}
}

func (t *Transport) answerHTTP(conn io.Conn, req *httpRequest) bool {
	if req.method != "POST" {
		return writeHTTP(conn, statusMethodNotAllowed, nil) == nil
	}
	resp := t.srv.Handle(req.body)
	if err := writeHTTP(conn, resp.Status, resp.Body); err != nil {
		return false
	}
	return !strings.EqualFold(req.headers["connection"], "close")
}

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

type subscription struct {
	id     string
	kind   string
	filter logFilter
}

type socket struct {
	id      uint64
	conn    io.Conn
	writeMu sync.Mutex
	subs    map[string]*subscription
}

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xa
)

func (sk *socket) send(op byte, payload []byte) error {
	sk.writeMu.Lock()
	defer sk.writeMu.Unlock()
	frame := make([]byte, 0, len(payload)+10)
	frame = append(frame, 0x80|op)
	switch {
	case len(payload) <= wsControlMax:
		frame = append(frame, byte(len(payload)))
	case len(payload) <= 0xffff:
		frame = append(frame, 126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	default:
		frame = append(frame, 127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(len(payload)))
	}
	frame = append(frame, payload...)
	_, err := sk.conn.Write(frame)
	return err
}

func (t *Transport) serveSocket(conn io.Conn, r *connReader, key string) {
	hello := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + acceptKey(key) + "\r\n\r\n"
	if _, err := conn.Write([]byte(hello)); err != nil {
		return
	}
	t.mu.Lock()
	t.socketSeq++
	sk := &socket{id: t.socketSeq, conn: conn, subs: map[string]*subscription{}}
	t.sockets[sk.id] = sk
	t.mu.Unlock()
	defer t.dropSocket(sk)
	for range wsMessagesMax {
		msg, err := readMessage(r, sk)
		if err != nil {
			return
		}
		if msg == nil {
			continue
		}
		if !t.answerSocket(sk, msg) {
			return
		}
	}
}

func (t *Transport) dropSocket(sk *socket) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sockets, sk.id)
}

type frame struct {
	fin     bool
	op      byte
	payload []byte
}

func readFrame(r *connReader) (frame, error) {
	head, err := r.take(2)
	if err != nil {
		return frame{}, err
	}
	f := frame{fin: head[0]&0x80 != 0, op: head[0] & 0x0f}
	masked := head[1]&0x80 != 0
	size, err := frameSize(r, uint64(head[1]&0x7f))
	if err != nil {
		return frame{}, err
	}
	var mask []byte
	if masked {
		if mask, err = r.take(4); err != nil {
			return frame{}, err
		}
	}
	if f.payload, err = r.take(int(size)); err != nil {
		return frame{}, err
	}
	if masked {
		for i := range f.payload {
			f.payload[i] ^= mask[i%4]
		}
	}
	return f, nil
}

func frameSize(r *connReader, short uint64) (uint64, error) {
	size := short
	switch short {
	case 126:
		ext, err := r.take(2)
		if err != nil {
			return 0, err
		}
		size = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext, err := r.take(8)
		if err != nil {
			return 0, err
		}
		size = binary.BigEndian.Uint64(ext)
	}
	if size > bodyMax {
		return 0, ErrFrameTooLarge
	}
	return size, nil
}

func readMessage(r *connReader, sk *socket) ([]byte, error) {
	var msg []byte
	for range wsFragmentsMax {
		f, err := readFrame(r)
		if err != nil {
			return nil, err
		}
		switch f.op {
		case opPing:
			if err := sk.send(opPong, f.payload); err != nil {
				return nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			if err := sk.send(opClose, f.payload); err != nil {
				return nil, err
			}
			return nil, io.ErrConnClosed
		case opText, opBinary, opContinuation:
		default:
			return nil, ErrBadFrame
		}
		msg = append(msg, f.payload...)
		if len(msg) > bodyMax {
			return nil, ErrFrameTooLarge
		}
		if f.fin {
			return msg, nil
		}
	}
	return nil, ErrFrameTooLarge
}

func (t *Transport) answerSocket(sk *socket, msg []byte) bool {
	if out, handled := t.subscriptionCall(sk, msg); handled {
		return sk.send(opText, out) == nil
	}
	resp := t.srv.Handle(msg)
	if bytes.Equal(resp.Body, nullBody) {
		return true
	}
	return sk.send(opText, resp.Body) == nil
}

func (t *Transport) subscriptionCall(sk *socket, msg []byte) ([]byte, bool) {
	var env envelope
	if json.Unmarshal(msg, &env) != nil {
		return nil, false
	}
	c, err := parseCall(&env)
	if err != nil {
		return nil, false
	}
	if c.notify {
		return nil, false
	}
	var result []byte
	switch c.method {
	case "eth_subscribe":
		result, err = t.subscribe(sk, c.params)
	case "eth_unsubscribe":
		result, err = unsubscribe(sk, c.params)
	default:
		return nil, false
	}
	w := newWriter(responseHint)
	if err != nil {
		writeError(w, c.id, asError(err))
		return w.bytes(), true
	}
	w.objOpen()
	w.kStr("jsonrpc", "2.0")
	w.key("id")
	w.raw(c.id)
	w.key("result")
	w.raw(result)
	w.objClose()
	return w.bytes(), true
}

func (t *Transport) subscribe(sk *socket, raw []byte) ([]byte, error) {
	p, err := newParams(raw)
	if err != nil {
		return nil, err
	}
	kindRaw, err := p.required("kind")
	if err != nil {
		return nil, err
	}
	kind, err := stringParam(kindRaw, "a string")
	if err != nil {
		return nil, err
	}
	sub := &subscription{kind: kind}
	switch kind {
	case "newHeads":
	case "logs":
		if raw := p.optional("params"); raw != nil {
			if sub.filter, err = filterParam(raw); err != nil {
				return nil, err
			}
		}
	default:
		return nil, invalidParams("unknown variant `" + kind + "`, expected one of `newHeads`, `logs`, `newPendingTransactions`, `syncing`")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(sk.subs) >= subscriptionsMax {
		return nil, newError(CodeServer, "too many subscriptions")
	}
	t.subSeq++
	sub.id = "0x" + strconv.FormatUint(t.subSeq, 16)
	sk.subs[sub.id] = sub
	return stringResult(sub.id), nil
}

func unsubscribe(sk *socket, raw []byte) ([]byte, error) {
	p, err := newParams(raw)
	if err != nil {
		return nil, err
	}
	idRaw, err := p.required("id")
	if err != nil {
		return nil, err
	}
	id, err := stringParam(idRaw, "a string")
	if err != nil {
		return nil, err
	}
	sk.writeMu.Lock()
	defer sk.writeMu.Unlock()
	if _, ok := sk.subs[id]; !ok {
		return resultFalse, nil
	}
	delete(sk.subs, id)
	return resultTrue, nil
}

func (t *Transport) Publish(b *BlockData) error {
	var err error
	for _, sk := range t.openSockets() {
		err = errors.Join(err, publishTo(sk, b))
	}
	return err
}

func publishTo(sk *socket, b *BlockData) error {
	for _, msg := range notifications(sk, b) {
		if err := sk.send(opText, msg); err != nil {
			return errors.Join(err, sk.conn.Close())
		}
	}
	return nil
}

func notifications(sk *socket, b *BlockData) [][]byte {
	sk.writeMu.Lock()
	subs := make([]subscription, 0, len(sk.subs))
	for _, id := range slices.Sorted(maps.Keys(sk.subs)) {
		subs = append(subs, *sk.subs[id])
	}
	sk.writeMu.Unlock()
	var out [][]byte
	for i := range subs {
		switch subs[i].kind {
		case "newHeads":
			out = append(out, notification(subs[i].id, func(w *writer) { writeHeader(w, b) }))
		case "logs":
			out = append(out, logNotifications(&subs[i], b)...)
		}
	}
	return out
}

func logNotifications(sub *subscription, b *BlockData) [][]byte {
	var out [][]byte
	logIndex := uint64(0)
	for i := range b.Receipts {
		r := &b.Receipts[i]
		for j := range r.Logs {
			if sub.filter.matches(&r.Logs[j]) {
				pos := logPos{b: b, txIndex: i, logIndex: logIndex + uint64(j)}
				out = append(out, notification(sub.id, func(w *writer) { writeLog(w, pos, &r.Logs[j]) }))
			}
		}
		logIndex += uint64(len(r.Logs))
	}
	return out
}

func notification(id string, result func(w *writer)) []byte {
	return encode(func(w *writer) {
		w.objOpen()
		w.kStr("jsonrpc", "2.0")
		w.kStr("method", "eth_subscription")
		w.key("params")
		w.objOpen()
		w.kStr("subscription", id)
		w.key("result")
		result(w)
		w.objClose()
		w.objClose()
	})
}
