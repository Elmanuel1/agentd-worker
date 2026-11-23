// Package resolver relays DNS queries to an upstream resolver. On Docker, a sandbox under runsc
// cannot reach the engine's embedded resolver at 127.0.0.11, which works only through rules in
// the container's own network namespace; a relay on the sandbox network, under runc, can.
package resolver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// timeout bounds one query's round trip to the upstream.
const timeout = 5 * time.Second

// Serve relays every UDP and TCP query on addr to upstream until ctx ends.
func Serve(ctx context.Context, addr, upstream string, log *slog.Logger) error {
	var lc net.ListenConfig
	packets, err := lc.ListenPacket(ctx, "udp", addr)
	if err != nil {
		return err
	}
	streams, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		packets.Close()
		return err
	}
	closeAll := func() { packets.Close(); streams.Close() }
	var wg sync.WaitGroup
	defer wg.Wait()
	// Runs before the wait on every return, so relayPackets ends however Accept failed.
	defer closeAll()
	stop := context.AfterFunc(ctx, closeAll)
	defer stop()
	slots := newLimit(inFlight, log)
	wg.Go(func() { relayPackets(packets, upstream, slots, log, &wg) })
	for {
		conn, err := streams.Accept()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		if !slots.take() {
			conn.Close()
			continue
		}
		wg.Go(func() {
			defer slots.release()
			relayStream(conn, upstream, log)
		})
	}
}

func relayPackets(packets net.PacketConn, upstream string, slots *limit, log *slog.Logger, wg *sync.WaitGroup) {
	buf := make([]byte, 65535)
	for {
		n, from, err := packets.ReadFrom(buf)
		if err != nil {
			return
		}
		if !slots.take() {
			continue
		}
		query := append([]byte(nil), buf[:n]...)
		wg.Go(func() {
			defer slots.release()
			answer, err := exchange(query, upstream)
			if err != nil {
				log.Debug("dns query failed", "error", err)
				return
			}
			packets.WriteTo(answer, from)
		})
	}
}

func exchange(query []byte, upstream string) ([]byte, error) {
	conn, err := net.DialTimeout("udp", upstream, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	return buf[:n], err
}

// relayStream copies a TCP query and its answer whole; DNS over TCP frames itself.
func relayStream(client net.Conn, upstream string, log *slog.Logger) {
	defer client.Close()
	server, err := net.DialTimeout("tcp", upstream, timeout)
	if err != nil {
		log.Debug("dns upstream unreachable", "error", err)
		return
	}
	defer server.Close()
	deadline := time.Now().Add(timeout)
	client.SetDeadline(deadline)
	server.SetDeadline(deadline)
	var wg sync.WaitGroup
	wg.Go(func() { io.Copy(server, client) })
	io.Copy(client, server)
	client.Close()
	wg.Wait()
}
