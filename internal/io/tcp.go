package io

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"
)

var ErrTailscaleDial = errors.New("io: tailscale local API refused the dial")

type netConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (c *netConn) Read(p []byte) (int, error) {
	if c.reader != nil && c.reader.Buffered() > 0 {
		return c.reader.Read(p)
	}
	return c.conn.Read(p)
}

func (c *netConn) Write(p []byte) (int, error) { return c.conn.Write(p) }

func (c *netConn) Close() error { return c.conn.Close() }

func (c *netConn) SetReadTimeout(nanos uint64) error {
	if nanos == 0 {
		return c.conn.SetReadDeadline(time.Time{})
	}
	return c.conn.SetReadDeadline(time.Now().Add(time.Duration(nanos)))
}

type TCPDialer struct {
	TimeoutNanos uint64
}

func (d TCPDialer) Dial(address string) (Conn, error) {
	c, err := net.DialTimeout("tcp", address, time.Duration(d.TimeoutNanos))
	if err != nil {
		return nil, err
	}
	return &netConn{conn: c}, nil
}

type TailscaleDialer struct {
	Socket       string
	TimeoutNanos uint64
}

func (d TailscaleDialer) Dial(address string) (Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	c, err := net.DialTimeout("unix", d.Socket, time.Duration(d.TimeoutNanos))
	if err != nil {
		return nil, err
	}
	if err := c.SetDeadline(time.Now().Add(time.Duration(d.TimeoutNanos))); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	request := "POST /localapi/v0/dial HTTP/1.1\r\nHost: local-tailscaled.sock\r\nConnection: upgrade\r\nUpgrade: ts-dial\r\nDial-Host: " +
		host + "\r\nDial-Port: " + port + "\r\n\r\n"
	if _, err := c.Write([]byte(request)); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	reader := bufio.NewReader(c)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		return nil, errors.Join(err, c.Close())
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		return nil, errors.Join(ErrTailscaleDial, errors.New(response.Status), c.Close())
	}
	if err := c.SetDeadline(time.Time{}); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	return &netConn{conn: c, reader: reader}, nil
}
