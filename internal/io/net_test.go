package io_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/gakonst/goempo/internal/io"
)

func TestPipeCarriesBytesBothWays(t *testing.T) {
	pair := io.Pipe()
	if _, err := pair.A.Write([]byte("hello ")); err != nil {
		t.Fatal(err)
	}
	if _, err := pair.A.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 0, 11)
	buf := make([]byte, 4)
	for range 4 {
		n, err := pair.B.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Equal(got, []byte("hello world")) {
		t.Fatalf("got %q", got)
	}
	if _, err := pair.B.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	n, err := pair.A.Read(buf)
	if err != nil || n != 1 || buf[0] != 1 {
		t.Fatalf("reverse read %d %v", n, err)
	}
}

func TestPipeCloseDrainsThenFails(t *testing.T) {
	pair := io.Pipe()
	if _, err := pair.A.Write([]byte{9}); err != nil {
		t.Fatal(err)
	}
	if err := pair.A.Close(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if n, err := pair.B.Read(buf); err != nil || n != 1 {
		t.Fatalf("drain %d %v", n, err)
	}
	if _, err := pair.B.Read(buf); !errors.Is(err, io.ErrConnClosed) {
		t.Fatalf("after close: %v", err)
	}
	if _, err := pair.B.Write(buf); !errors.Is(err, io.ErrConnClosed) {
		t.Fatalf("write after close: %v", err)
	}
}

func TestMemNetworkDialsListener(t *testing.T) {
	network := io.NewMemNetwork()
	if _, err := network.Dial("a:1"); !errors.Is(err, io.ErrRefused) {
		t.Fatalf("dial nothing: %v", err)
	}
	l, err := network.Listen("a:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := network.Listen("a:1"); !errors.Is(err, io.ErrAddressInUse) {
		t.Fatalf("double listen: %v", err)
	}
	client, err := network.Dial("a:1")
	if err != nil {
		t.Fatal(err)
	}
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if n, err := server.Read(buf); err != nil || string(buf[:n]) != "ping" {
		t.Fatalf("server read %q %v", buf[:n], err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Accept(); !errors.Is(err, io.ErrConnClosed) {
		t.Fatalf("accept after close: %v", err)
	}
	if _, err := network.Dial("a:1"); !errors.Is(err, io.ErrRefused) {
		t.Fatalf("dial closed: %v", err)
	}
}
