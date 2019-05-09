//go:generate mkdir -p ./certs
//go:generate openssl req -new -nodes -x509 -out certs/server.pem -keyout certs/server.key -days 3650 -subj "/C=US/ST=TX/L=Earth/O=Cube/OU=IT"

package msgprpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/vmihailenco/msgpack"
)

const (
	Version    = 0.4
	MinVersion = 0.3

	ErrClientVersionTooLow = "client version too low"
	ErrInvalidKey          = "invalid key"
	ErrNotFound            = "endpoint not found"
)

type ctxKey struct{ uint8 }

var (
	errRetry      = errors.New("retry")
	errTooLow     = &handshakeResponse{Version, ErrClientVersionTooLow}
	errInvalidKey = &handshakeResponse{Version, ErrInvalidKey}
	errNotFound   = &call{Error: ErrNotFound}
	okHandshake   = &handshakeResponse{Version: Version}

	ConnKey    = ctxKey{0}
	EncoderKey = ctxKey{1}
	DecoderKey = ctxKey{2}
)

func init() {
	os.Setenv("GODEBUG", "tls13=1")

	msgpack.RegisterExt(-127, (*handshake)(nil))
	msgpack.RegisterExt(-126, (*handshakeResponse)(nil))
	msgpack.RegisterExt(-125, (*call)(nil))
}

// RegisterType is an alias for msgpack.RegisterExt
// allows you to send and receive structs, the id must be the same on both the client and server
func RegisterType(id int8, v interface{}) {
	msgpack.RegisterExt(id, v)
}

type Handler func(ctx context.Context, args ...interface{}) ([]interface{}, error)

func New() *Server { return NewWithContext(context.Background()) }

func NewWithContext(ctx context.Context) *Server {
	ctx, cfn := context.WithCancel(ctx)
	return &Server{
		m:        map[string]Handler{},
		ctx:      ctx,
		cancelFn: cfn,
	}
}

type Server struct {
	m        map[string]Handler
	ctx      context.Context
	cancelFn context.CancelFunc
	Logger   interface {
		Output(int, string) error
	}

	AuthFn func(key string) bool

	NotFoundHandler func(ctx context.Context, endpoint string, args ...interface{}) ([]interface{}, error)
}

func (s *Server) On(endpoint string, h Handler) {
	s.m[endpoint] = h
}

func (s *Server) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	return s.Serve(l)
}

func (s *Server) ListenAndServeTLS(addr, cert, key string) error {
	crt, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return err
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{crt},
		MinVersion:   tls.VersionTLS13,

		PreferServerCipherSuites: true,
	}

	l, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return err
	}

	return s.Serve(l)
}

func (s *Server) Serve(l net.Listener) error {
	go func() {
		select {
		case <-s.ctx.Done():
			l.Close()
		}
	}()

	for {
		c, err := l.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			continue
		}
		go s.handle(c)
	}
}

func (s *Server) Close() error {
	s.cancelFn()
	return nil
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	// set a 1 second timeout for handshake, in case someone is trying to abuse the server
	conn.SetDeadline(time.Now().Add(time.Second))

	dec := msgpack.NewDecoder(conn)
	dec.UseJSONTag(true)
	enc := msgpack.NewEncoder(conn)
	enc.UseJSONTag(true)

	var hs handshake
	if err := dec.Decode(&hs); err != nil {
		s.logf("handshake error (%s, %+v): %v", conn.RemoteAddr(), &hs, err)
		return
	}

	if hs.Version < MinVersion {
		enc.Encode(errTooLow)
		return
	}

	if s.AuthFn != nil && !s.AuthFn(hs.AuthKey) {
		enc.Encode(errInvalidKey)
		return
	}

	if err := enc.Encode(okHandshake); err != nil {
		s.logf("handshake error (%s, %+v): %v", conn.RemoteAddr(), &hs, err)
		return
	}

	conn.SetDeadline(time.Time{})

	s.logf("new connection from %s", conn.RemoteAddr())

	ctx := &srvCtx{
		Context: s.ctx,
		conn:    conn,
		enc:     enc,
		dec:     dec,
	}

	for {
		var c call
		if err := dec.Decode(&c); err != nil {
			if err != io.EOF {
				s.logf("decode error (%+v): %v", c, err)
			}
			break
		}

		if s.ctx.Err() != nil {
			break
		}

		h := s.m[c.Endpoint]

		if h == nil && s.NotFoundHandler != nil {
			h = func(ctx context.Context, args ...interface{}) ([]interface{}, error) {
				return s.NotFoundHandler(ctx, c.Endpoint, args...)
			}
		}

		if h == nil {
			enc.Encode(errNotFound)
			break
		}

		resp, err := h(ctx, c.Args...)
		if err != nil {
			c.Error = err.Error()
		}

		c.Endpoint, c.Args = "", resp
		if err := enc.Encode(&c); err != nil {
			s.logf("encode error (%+v): %v", c, err)
			break
		}
	}
}

func (s *Server) logf(f string, args ...interface{}) {
	if s.Logger != nil {
		s.Logger.Output(2, fmt.Sprintf("[msgprpc] "+f, args...))
	}
}

func GetConn(ctx context.Context) net.Conn {
	if ctx, ok := ctx.(*srvCtx); ok {
		return ctx.conn
	}
	v, _ := ctx.Value(ConnKey).(net.Conn)
	return v
}

func GetEncoder(ctx context.Context) *msgpack.Encoder {
	if ctx, ok := ctx.(*srvCtx); ok {
		return ctx.enc
	}
	v, _ := ctx.Value(EncoderKey).(*msgpack.Encoder)
	return v
}

func GetDecoder(ctx context.Context) *msgpack.Decoder {
	if ctx, ok := ctx.(*srvCtx); ok {
		return ctx.dec
	}
	v, _ := ctx.Value(DecoderKey).(*msgpack.Decoder)
	return v
}

type handshake struct {
	Version float64 `msgpack:"v,omitempty"`
	Flags   uint64  `msgpack:"f,omitempty"`
	AuthKey string  `msgpack:"ak,omitempty"`
}

type handshakeResponse struct {
	Version float64 `msgpack:"v,omitempty"`
	Error   string  `msgpack:"e,omitempty"`
}

type call struct {
	Endpoint string        `msgpack:"ep,omitempty"`
	Args     []interface{} `msgpack:"a,omitempty"`
	Error    string        `msgpack:"e,omitempty"`
}

type srvCtx struct {
	context.Context
	conn net.Conn
	enc  *msgpack.Encoder
	dec  *msgpack.Decoder
}

func (c *srvCtx) Value(key interface{}) interface{} {
	switch key {
	case ConnKey:
		return c.conn
	case EncoderKey:
		return c.enc
	case DecoderKey:
		return c.dec
	default:
		return c.Context.Value(key)
	}
}
