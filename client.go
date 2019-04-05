package msgprpc

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/vmihailenco/msgpack"
)

func NewClient(addr string) (*Client, error) {
	c := &Client{addr: addr}
	if err := c.init(); err != nil {
		return nil, err
	}
	return c, nil
}

type Client struct {
	mux  sync.RWMutex
	addr string
	conn net.Conn
	enc  *msgpack.Encoder
	dec  *msgpack.Decoder
}

func (c *Client) init() (err error) {
	c.mux.Lock()
	defer c.mux.Unlock()

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

	enc := msgpack.NewEncoder(conn)
	enc.UseJSONTag(true)

	dec := msgpack.NewDecoder(conn)
	dec.UseJSONTag(true)

	if err := enc.Encode(&handshake{Version: Version}); err != nil {
		return err
	}

	var hr handshakeResponse
	if err := dec.Decode(&hr); err != nil {
		return err
	}

	if hr.Error != "" {
		return errors.New(hr.Error)
	}

	c.conn, c.enc, c.dec = conn, enc, dec

	return nil
}

func (c *Client) Call(ctx context.Context, endpoint string, args ...interface{}) (out []interface{}, err error) {
	var retried bool

RETRY:
	c.mux.RLock()
	conn := c.conn
	if conn != nil {
		out, err = c.call(endpoint, args...)
	}
	c.mux.RUnlock()

	if (shouldRetry(err) || conn == nil) && !retried {
		log.Println("retry")
		if err = c.init(); err != nil {
			return
		}
		retried = true
		goto RETRY
	}

	return
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
	if _, ok := err.(net.Error); ok {
		return true
	}

	return false
}
