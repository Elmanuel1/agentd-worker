package resolver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// upstream echoes each UDP datagram and TCP stream with "answer:" in front.
func upstream(t *testing.T) string {
	t.Helper()
	packets, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	streams, err := net.Listen("tcp", packets.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { packets.Close(); streams.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := packets.ReadFrom(buf)
			if err != nil {
				return
			}
			packets.WriteTo(append([]byte("answer:"), buf[:n]...), from)
		}
	}()
	go func() {
		for {
			c, err := streams.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(io.LimitReader(c, 5))
			c.Write(append([]byte("answer:"), b...))
			c.Close()
		}
	}()
	return packets.LocalAddr().String()
}

// TestRelay holds DK-2's DNS path: a query over UDP and one over TCP come back from upstream.
func TestRelay(t *testing.T) {
	up := upstream(t)
	addr := "127.0.0.1:45353"
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error)
	go func() { done <- Serve(ctx, addr, up, slog.New(slog.NewJSONHandler(io.Discard, nil))) }()
	time.Sleep(200 * time.Millisecond)
	for _, network := range []string{"udp", "tcp"} {
		c, err := net.Dial(network, addr)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(5 * time.Second))
		c.Write([]byte("query"))
		buf := make([]byte, 64)
		n, err := io.ReadAtLeast(c, buf, len("answer:query"))
		if err != nil || string(buf[:n]) != "answer:query" {
			t.Errorf("%s: got %q, %v", network, buf[:n], err)
		}
		c.Close()
	}
	cancel()
	if err := <-done; err != nil {
		t.Error(err)
	}
}
