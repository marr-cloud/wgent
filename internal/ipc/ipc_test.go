package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/marr-cloud/wgent/internal/api"
)

type echoHandler struct{}

func (echoHandler) Handle(_ context.Context, req api.Request) api.Response {
	if req.Tunnel == "missing" {
		return api.Response{Error: &api.Error{Code: api.CodeTunnelNotFound, Message: "no such tunnel"}}
	}
	return api.Response{Status: &api.Status{Tunnel: req.Tunnel, State: "up"}}
}

func startServer(t *testing.T) (net.Listener, context.CancelFunc, chan error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, echoHandler{}) }()
	t.Cleanup(cancel)
	return l, cancel, done
}

func client(l net.Listener) Client {
	return Client{Dial: func() (net.Conn, error) { return net.Dial("tcp", l.Addr().String()) }}
}

func TestRoundTrip(t *testing.T) {
	l, _, _ := startServer(t)
	resp, err := client(l).Do(api.Request{Op: api.OpStatus, Tunnel: "home"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProtocolVersion != api.ProtocolVersion {
		t.Errorf("protocol version = %d", resp.ProtocolVersion)
	}
	if resp.Status == nil || resp.Status.Tunnel != "home" || resp.Status.State != "up" {
		t.Errorf("unexpected status: %+v", resp.Status)
	}
}

func TestServerErrorReturnedAsError(t *testing.T) {
	l, _, _ := startServer(t)
	resp, err := client(l).Do(api.Request{Op: api.OpUp, Tunnel: "missing"})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error", err)
	}
	if apiErr.Code != api.CodeTunnelNotFound {
		t.Errorf("code = %q", apiErr.Code)
	}
	if resp.Error == nil {
		t.Error("response should carry the error too")
	}
}

func TestProtocolMismatch(t *testing.T) {
	l, _, _ := startServer(t)
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"protocol_version":99,"op":"list"}`)); err != nil {
		t.Fatal(err)
	}
	var resp api.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != api.CodeProtocolMismatch {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Error.Hint != "sudo wgent service install" {
		t.Errorf("hint = %q", resp.Error.Hint)
	}
}

func TestMalformedJSON(t *testing.T) {
	l, _, _ := startServer(t)
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte(`{not json`))
	var resp api.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != api.CodeUsage {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestServeStopsOnCancel(t *testing.T) {
	_, cancel, done := startServer(t)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}
