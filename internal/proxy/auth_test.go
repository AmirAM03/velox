package proxy

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/AmirAM03/velox/internal/model"
)

func TestServer_Auth(t *testing.T) {
	sel := NewSelector(nil, 5, 2, nil)
	sel.SetPool([]*model.ProxyConfig{{ID: "a"}})
	srv := NewServer("127.0.0.1", 0, "alice", "s3cret", &mockEngine{}, sel, nil)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	addr := srv.Addr().String()

	socks := func(user, pass string, methods ...byte) []byte {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.Write(append([]byte{0x05, byte(len(methods))}, methods...))
		sel := make([]byte, 2)
		if _, err := io.ReadFull(c, sel); err != nil || sel[1] != 0x02 {
			return sel
		}
		msg := append([]byte{0x01, byte(len(user))}, user...)
		msg = append(append(msg, byte(len(pass))), pass...)
		c.Write(msg)
		res := make([]byte, 2)
		io.ReadFull(c, res)
		return res
	}
	if r := socks("", "", 0x00); r[1] != 0xFF {
		t.Errorf("no-auth SOCKS5 client: got method %#x, want 0xFF", r[1])
	}
	if r := socks("alice", "wrong", 0x02); r[1] != 0x01 {
		t.Errorf("bad password: got status %#x, want 0x01", r[1])
	}
	if r := socks("alice", "s3cret", 0x00, 0x02); r[1] != 0x00 {
		t.Errorf("good password: got status %#x, want 0x00", r[1])
	}

	httpStatus := func(auth string) int {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		req := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n"
		if auth != "" {
			req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(auth)) + "\r\n"
		}
		c.Write([]byte(req + "\r\n"))
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if got := httpStatus(""); got != http.StatusProxyAuthRequired {
		t.Errorf("HTTP without auth: got %d, want 407", got)
	}
	if got := httpStatus("alice:nope"); got != http.StatusProxyAuthRequired {
		t.Errorf("HTTP bad auth: got %d, want 407", got)
	}
}
