package io

import "net"

type TCPListener struct {
	l net.Listener
}

func ListenTCP(address string) (*TCPListener, error) {
	l, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return &TCPListener{l: l}, nil
}

func (t *TCPListener) Address() string { return t.l.Addr().String() }

func (t *TCPListener) Accept() (Conn, error) {
	c, err := t.l.Accept()
	if err != nil {
		return nil, err
	}
	return &netConn{conn: c}, nil
}

func (t *TCPListener) Close() error { return t.l.Close() }
