package action

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPDialUsesValidatedDNSResult(t *testing.T) {
	s := fixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	done := make(chan struct{})
	var aQueries atomic.Int32
	go func() {
		defer close(done)
		buf := make([]byte, 512)
		for {
			n, addr, err := dns.ReadFrom(buf)
			if err != nil {
				return
			}
			q := append([]byte(nil), buf[:n]...)
			if n < 17 {
				continue
			}
			end := 12
			for end < n && q[end] != 0 {
				end += int(q[end]) + 1
			}
			end++
			if end+4 > n {
				continue
			}
			typ := binary.BigEndian.Uint16(q[end : end+2])
			end += 4
			response := append([]byte(nil), q[:end]...)
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			binary.BigEndian.PutUint16(response[6:8], 0)
			binary.BigEndian.PutUint16(response[8:10], 0)
			binary.BigEndian.PutUint16(response[10:12], 0)
			if typ == 1 {
				ipLast := byte(1)
				if aQueries.Add(1) > 1 {
					ipLast = 2
				}
				binary.BigEndian.PutUint16(response[6:8], 1)
				response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4, 127, 0, 0, ipLast)
			}
			_, _ = dns.WriteTo(response, addr)
		}
	}()
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", dns.LocalAddr().String())
	}}
	defer func() { net.DefaultResolver = previous; dns.Close(); <-done }()
	r := httpRecord("http://pinned-action.test:" + port + "/")
	Normalize(&r)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	v := s.executeHTTP(ctx, r, Request{})
	if v.status != "succeeded" || requests.Load() != 1 || aQueries.Load() != 1 {
		t.Fatal("执行未使用首次校验的 DNS IP", v.status, v.message, requests.Load(), strconv.Itoa(int(aQueries.Load())))
	}
}
func TestHTTPKeepsTLSHostnameValidation(t *testing.T) {
	s := fixture(t)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	r := httpRecord(server.URL)
	Normalize(&r)
	v := s.executeHTTP(t.Context(), r, Request{})
	if v.status != "failed" || requests.Load() != 0 {
		t.Fatal("接受了未信任 TLS 证书", v)
	}
}
