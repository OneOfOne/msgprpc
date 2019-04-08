package msgprpc

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"

	"github.com/vmihailenco/msgpack"
)

func NewClient(addr string) (*Client, error) {
	c := &Client{
		addr: addr,
		dec:  msgpack.NewDecoder(nil).UseJSONTag(true),
	}

	if err := c.reinit(); err != nil {
		return nil, err
	}

	return c, nil
}

type Client struct {
	mux  sync.Mutex
	addr string
	conn net.Conn
	enc  *msgpack.Encoder
	dec  *msgpack.Decoder
}

func (c *Client) reinit() (err error) {
	if c.conn != nil {
		c.conn.Close()
	}

	var (
		addr            = c.addr
		isTLS, insecure = strings.HasPrefix(c.addr, "tls://"), strings.HasPrefix(c.addr, "tls+insecure://")
		conn            net.Conn
	)

	if isTLS || insecure {
		cfg := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: insecure}
		if isTLS {
			addr = addr[6:]
		} else {
			addr = addr[15:]
		}
		conn, err = tls.Dial("tcp", addr, cfg)
	} else {
		conn, err = net.Dial("tcp", c.addr)
	}

	if err != nil {
		return err
	}

	c.conn = conn

	c.dec.Reset(conn)

	c.enc = msgpack.NewEncoder(conn).UseJSONTag(true)

	if err := c.enc.Encode(&handshake{Version: Version}); err != nil {
		return err
	}

	var hr handshakeResponse
	if err := c.dec.Decode(&hr); err != nil {
		return err
	}

	if hr.Error != "" {
		return errors.New(hr.Error)
	}

	return nil
}

type Args []interface{}

func (c *Client) Call(ctx context.Context, endpoint string, args ...interface{}) (out Args, err error) {
	done := make(chan struct{})
	go func() {
		c.mux.Lock()
		defer c.mux.Unlock()
		if out, err = c.call(endpoint, args...); shouldRetry(err) {
			if err = c.reinit(); err == nil {
				out, err = c.call(endpoint, args...)
			}
		}

		close(done)
	}()

	select {
	case <-done:
		return
	case <-ctx.Done():
		c.conn.Close() // kill the connection to free the mutex
		return nil, ctx.Err()
	}
}

func (c *Client) BatchCall(ctx context.Context, endpoint string, allArgs []Args) (out []Args, errs []error) {
	done := make(chan struct{})
	out = make([]Args, len(allArgs))
	errs = make([]error, len(allArgs))
	go func() {
		for i := range allArgs {
			out[i], errs[i] = c.Call(ctx, endpoint, allArgs[i]...)
		}
		close(done)
	}()

	select {
	case <-done:
		return
	case <-ctx.Done():
		return nil, []error{ctx.Err()}
	}
}

func (c *Client) Close() (err error) {
	c.mux.Lock()
	defer c.mux.Unlock()
	if c.conn != nil {
		err = c.conn.Close()
		c.conn = nil
	}
	return
}

func (c *Client) call(endpoint string, args ...interface{}) ([]interface{}, error) {
	if c.conn == nil {
		return nil, errRetry
	}

	if err := c.enc.Encode(&call{endpoint, args, ""}); err != nil {
		return nil, err
	}

	var cc call
	if err := c.dec.Decode(&cc); err != nil {
		return nil, err
	}

	if cc.Error != "" {
		return nil, errors.New(cc.Error)
	}

	return cc.Args, nil
}

func shouldRetry(err error) bool {
	if err == nil {
		return false
	}

	if err == errRetry {
		return true
	}

	if _, ok := err.(net.Error); ok {
		return true
	}

	return false
}
