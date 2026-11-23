// Package ipc implements the one-request/one-response JSON transport between
// the wgent CLI and service.
package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/marr-cloud/wgent/internal/api"
)

const (
	readTimeout    = 10 * time.Second
	defaultTimeout = 30 * time.Second
	installHint    = "sudo wgent service install"
	// maxRequest bounds what the SYSTEM service reads from one connection.
	maxRequest = 64 << 10
)

type Handler interface {
	Handle(ctx context.Context, req api.Request) api.Response
}

// Serve accepts connections until ctx is cancelled, then closes l.
func Serve(ctx context.Context, l net.Listener, h Handler) error {
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go handleConn(ctx, conn, h)
	}
}

func handleConn(ctx context.Context, conn net.Conn, h Handler) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

	var resp api.Response
	var req api.Request
	if err := json.NewDecoder(io.LimitReader(conn, maxRequest)).Decode(&req); err != nil {
		resp = errResp(api.CodeUsage, "malformed request: "+err.Error(), "")
	} else if req.ProtocolVersion != api.ProtocolVersion {
		resp = errResp(api.CodeProtocolMismatch, "protocol version mismatch between wgent client and service", installHint)
	} else {
		resp = safeHandle(ctx, h, req)
	}
	resp.ProtocolVersion = api.ProtocolVersion
	_ = conn.SetWriteDeadline(time.Now().Add(readTimeout))
	_ = json.NewEncoder(conn).Encode(resp)
}

// safeHandle runs h.Handle, turning a panic into an internal error so one bad
// request cannot take down the SYSTEM service.
func safeHandle(ctx context.Context, h Handler, req api.Request) (resp api.Response) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("wgent: panic handling %s %q: %v", req.Op, req.Tunnel, r)
			resp = errResp(api.CodeInternal, fmt.Sprintf("internal error: %v", r), "")
		}
	}()
	return h.Handle(ctx, req)
}

func errResp(code, msg, hint string) api.Response {
	return api.Response{Error: &api.Error{Code: code, Message: msg, Hint: hint}}
}

type Dialer func() (net.Conn, error)

type Client struct {
	Dial    Dialer
	Timeout time.Duration
}

// Do sends req and returns the response. If the server reports an error, it is
// returned both in resp.Error and as the error value (an *api.Error).
func (c Client) Do(req api.Request) (api.Response, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	conn, err := c.Dial()
	if err != nil {
		return api.Response{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	req.ProtocolVersion = api.ProtocolVersion
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return api.Response{}, err
	}
	var resp api.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		var ne net.Error
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &ne) && ne.Timeout()) {
			return api.Response{}, &api.Error{Code: api.CodeServiceNotInstalled, Message: "wgent service not responding", Hint: installHint}
		}
		return api.Response{}, err
	}
	if resp.Error != nil {
		return resp, resp.Error
	}
	return resp, nil
}
