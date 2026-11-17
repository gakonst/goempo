package rlpx

import (
	"encoding/binary"
	"errors"

	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/p2p/snappy"
	"github.com/gakonst/goempo/internal/rlp"
)

var ErrShortRead = errors.New("rlpx: connection ended inside a read")

type Msg struct {
	Code    uint64
	Payload []byte
}

type Conn struct {
	conn   io.Conn
	codec  *Codec
	snappy bool
}

func readFull(conn io.Conn, buf []byte) error {
	filled := 0
	reads := 0
	for ; filled < len(buf) && reads < len(buf); reads++ {
		n, err := conn.Read(buf[filled:])
		filled += n
		if err != nil && filled < len(buf) {
			return err
		}
	}
	if filled != len(buf) {
		return ErrShortRead
	}
	return nil
}

func readPacket(conn io.Conn) ([]byte, error) {
	var prefix [2]byte
	if err := readFull(conn, prefix[:]); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint16(prefix[:]))
	if size+2 > PacketMax {
		return nil, ErrPacket
	}
	packet := make([]byte, 2+size)
	copy(packet, prefix[:])
	if err := readFull(conn, packet[2:]); err != nil {
		return nil, err
	}
	return packet, nil
}

func Dial(conn io.Conn, h *Handshake) (*Conn, error) {
	auth, err := h.WriteAuth()
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(auth); err != nil {
		return nil, err
	}
	ack, err := readPacket(conn)
	if err != nil {
		return nil, err
	}
	if err := h.ReadAck(ack); err != nil {
		return nil, err
	}
	return finish(conn, h)
}

func Accept(conn io.Conn, h *Handshake) (*Conn, error) {
	auth, err := readPacket(conn)
	if err != nil {
		return nil, err
	}
	if err := h.ReadAuth(auth); err != nil {
		return nil, err
	}
	ack, err := h.WriteAck()
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(ack); err != nil {
		return nil, err
	}
	return finish(conn, h)
}

func finish(conn io.Conn, h *Handshake) (*Conn, error) {
	secrets, err := h.Secrets()
	if err != nil {
		return nil, err
	}
	codec, err := NewCodec(secrets)
	if err != nil {
		return nil, err
	}
	return &Conn{conn: conn, codec: codec}, nil
}

func (c *Conn) EnableSnappy() { c.snappy = true }

func (c *Conn) Close() error { return c.conn.Close() }

func (c *Conn) SetReadTimeout(nanos uint64) error { return c.conn.SetReadTimeout(nanos) }

func (c *Conn) WriteMsg(m Msg) error {
	payload := m.Payload
	if c.snappy {
		payload = snappy.Encode(payload)
	}
	data := append(rlp.EncodeUint64(m.Code), payload...)
	frame, err := c.codec.Seal(data)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(frame)
	return err
}

func (c *Conn) ReadMsg() (Msg, error) {
	var header [HeaderLength]byte
	if err := readFull(c.conn, header[:]); err != nil {
		return Msg{}, err
	}
	size, err := c.codec.OpenHeader(header[:])
	if err != nil {
		return Msg{}, err
	}
	body := make([]byte, BodyLength(size))
	if err := readFull(c.conn, body); err != nil {
		return Msg{}, err
	}
	data, err := c.codec.OpenBody(size, body)
	if err != nil {
		return Msg{}, err
	}
	s := rlp.NewStream(data)
	code, err := s.Uint64()
	if err != nil {
		return Msg{}, err
	}
	payload := s.Rest()
	if c.snappy {
		if payload, err = snappy.Decode(payload); err != nil {
			return Msg{}, err
		}
	}
	return Msg{Code: code, Payload: payload}, nil
}
