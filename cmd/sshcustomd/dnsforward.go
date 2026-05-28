//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// dnsForwarder accepts DNS queries on UDP and forwards them through the SSH
// tunnel as TCP DNS (RFC 7766). This is necessary because SSH cannot carry
// UDP, but every public DNS resolver supports DNS-over-TCP on port 53.
//
// The forwarder also caches responses for a short TTL so repeated lookups
// don't pay the full RTT through the tunnel every time.
type dnsForwarder struct {
	pool       *SSHPool
	upstream   []string
	conn       *net.UDPConn
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	cache      sync.Map
	queries    int64
	hits       int64
	tunnelErrs int64
}

type dnsCacheEntry struct {
	response []byte
	expires  time.Time
}

// startDNSForwarder opens a UDP listener on host:port and forwards queries
// over SSH-tunneled TCP. Returns a stop function and any startup error.
func startDNSForwarder(ctx context.Context, host string, port int, pool *SSHPool, upstreams []string, st *State) (func(), error) {
	if len(upstreams) == 0 {
		upstreams = []string{"1.1.1.1:53", "8.8.8.8:53"}
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	fwdCtx, cancel := context.WithCancel(ctx)
	f := &dnsForwarder{
		pool:     pool,
		upstream: upstreams,
		conn:     conn,
		cancel:   cancel,
	}
	st.set(func() {
		st.LastEvent = "DNS forwarder listening on " + addr + " (UDP -> TCP via SSH)"
	})
	log.Printf("DNS forwarder listening on udp %s; upstreams=%v", addr, upstreams)
	f.wg.Add(1)
	go f.serve(fwdCtx)
	return func() {
		cancel()
		_ = conn.Close()
		f.wg.Wait()
		log.Printf("DNS forwarder stopped (queries=%d cache_hits=%d errs=%d)",
			atomic.LoadInt64(&f.queries),
			atomic.LoadInt64(&f.hits),
			atomic.LoadInt64(&f.tunnelErrs))
	}, nil
}

func (f *dnsForwarder) serve(ctx context.Context) {
	defer f.wg.Done()
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = f.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, clientAddr, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if n < 12 {
			continue
		}
		query := append([]byte(nil), buf[:n]...)
		atomic.AddInt64(&f.queries, 1)
		go f.handle(ctx, query, clientAddr)
	}
}

func (f *dnsForwarder) handle(ctx context.Context, query []byte, clientAddr *net.UDPAddr) {
	if len(query) < 12 {
		return
	}
	key := string(query[2:])
	if cached, ok := f.cache.Load(key); ok {
		entry := cached.(*dnsCacheEntry)
		if time.Now().Before(entry.expires) {
			resp := append([]byte(nil), entry.response...)
			if len(resp) >= 2 {
				resp[0] = query[0]
				resp[1] = query[1]
			}
			_, _ = f.conn.WriteToUDP(resp, clientAddr)
			atomic.AddInt64(&f.hits, 1)
			return
		}
		f.cache.Delete(key)
	}

	resp, err := f.queryUpstream(ctx, query)
	if err != nil {
		atomic.AddInt64(&f.tunnelErrs, 1)
		return
	}
	_, _ = f.conn.WriteToUDP(resp, clientAddr)
	f.cache.Store(key, &dnsCacheEntry{
		response: append([]byte(nil), resp...),
		expires:  time.Now().Add(5 * time.Minute),
	})
}

func (f *dnsForwarder) queryUpstream(ctx context.Context, query []byte) ([]byte, error) {
	if f.pool == nil {
		return nil, errors.New("no SSH pool")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	var lastErr error
	for _, upstream := range f.upstream {
		conn, err := f.pool.Dial(dialCtx, "tcp", upstream)
		if err != nil {
			lastErr = err
			continue
		}

		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		lenPrefix := make([]byte, 2)
		binary.BigEndian.PutUint16(lenPrefix, uint16(len(query)))
		if _, err := conn.Write(lenPrefix); err != nil {
			conn.Close()
			lastErr = err
			continue
		}
		if _, err := conn.Write(query); err != nil {
			conn.Close()
			lastErr = err
			continue
		}

		respLenBuf := make([]byte, 2)
		if _, err := io.ReadFull(conn, respLenBuf); err != nil {
			conn.Close()
			lastErr = err
			continue
		}
		respLen := binary.BigEndian.Uint16(respLenBuf)
		if respLen == 0 || respLen > 4096 {
			conn.Close()
			lastErr = errors.New("invalid DNS response length")
			continue
		}
		resp := make([]byte, respLen)
		if _, err := io.ReadFull(conn, resp); err != nil {
			conn.Close()
			lastErr = err
			continue
		}
		conn.Close()
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no upstream DNS server reachable")
	}
	return nil, lastErr
}
