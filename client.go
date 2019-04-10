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

func NewClient(addr, key string) *Client {
	return &Client{
		addr: addr,
		key:  key,
		dec:  msgpack.NewDecoder(nil).UseJSONTag(true),
	}
}

type Client struct {
	mux  sync.Mutex
	addr string
	key  string
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

	if err := c.enc.Encode(&handshake{Version: Version, AuthKey: c.key}); err != nil {
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

type CallResult struct {
	Ret []interface{}
	Err error
}

func (c *Client) Call(ctx context.Context, endpoint string, args ...interface{}) ([]interface{}, error) {
	res := make(chan *CallResult, 1)

	go func() {
		c.mux.Lock()
		defer c.mux.Unlock()
		out, err := c.call(endpoint, args...)
		if shouldRetry(err) {
			if err = c.reinit(); err == nil {
				out, err = c.call(endpoint, args...)
			}
		}
		res <- &CallResult{out, err}
		close(res)
	}()

	select {
	case v := <-res:
		return v.Ret, v.Err
	case <-ctx.Done():
		return nil, ctx.Err()
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
