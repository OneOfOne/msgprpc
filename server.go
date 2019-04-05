//go:generate mkdir -p ./certs
//go:generate openssl req -new -nodes -x509 -out certs/server.pem -keyout certs/server.key -days 3650 -subj "/C=US/ST=TX/L=Earth/O=Meteora/OU=IT"

package msgprpc

import (
	"context"
	"crypto/tls"
	"net"
	"os"

	"github.com/vmihailenco/msgpack"
)

const (
	Version    = 1
	MinVersion = 1

	ErrClientVersionTooLow = "client version too low"
	ErrNotFound            = "endpoint not found"
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
	if id < 0 {
		panic("id < 0 reserved")
	}
	msgpack.RegisterExt(id, v)
}

var (
	errTooLow   = &handshakeResponse{Version, ErrClientVersionTooLow}
	errNotFound = &call{Error: ErrNotFound}
	okHandshake = &handshakeResponse{Version: Version}
)

type Handler func(ctx context.Context, args ...interface{}) ([]interface{}, error)

func NewServer(ctx context.Context) *Server {
	ctx, cfn := context.WithCancel(ctx)
	return &Server{
		m:        map[string]Handler{},
		ctx:      ctx,
		cancelFn: cfn}
}

type Server struct {
	m        map[string]Handler
	ctx      context.Context
	cancelFn context.CancelFunc
	Logger   interface {
		Printf(string, ...interface{})
	}
}

func (s *Server) On(endpoint string, h Handler) {
	s.m[endpoint] = h
}

func (s *Server) Listen(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	go s.Serve(l)
	return nil
}

func (s *Server) ListenTLS(addr string, cert, key string) error {
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

	go s.Serve(l)
	return nil
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
				return err
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

	dec := msgpack.NewDecoder(conn)
	dec.UseJSONTag(true)
	enc := msgpack.NewEncoder(conn)
	enc.UseJSONTag(true)

	var hs handshake
	if err := dec.Decode(&hs); err != nil {
		s.log("handshake error (%+v): %v", &hs, err)
		return
	}

	if hs.Version < MinVersion {
		enc.Encode(errTooLow)
		return
	}

	if err := enc.Encode(okHandshake); err != nil {
		s.log("handshake error (%+v): %v", &hs, err)
		return
	}

	for {
		var c call
		if err := dec.Decode(&c); err != nil {
			s.log("decode error (%+v): %v", c, err)
			break
		}

		if s.ctx.Err() != nil {
			break
		}

		h := s.m[c.Endpoint]

		if h == nil {
			enc.Encode(errNotFound)
			break
		}

		resp, err := h(s.ctx, c.Args...)
		if err != nil {
			c.Error = err.Error()
		}

		c.Endpoint, c.Args = "", resp
		if err := enc.Encode(&c); err != nil {
			s.log("encode error (%+v): %v", c, err)
			break
		}
	}
}

func (s *Server) log(f string, args ...interface{}) {
	if s.Logger != nil {
		s.Logger.Printf(f, args)
	}
}

type handshake struct {
	Version uint64 `msgpack:"v,omitempty"`
	Flags   uint64 `msgpack:"f,omitempty"`
}

type handshakeResponse struct {
	Version uint64 `msgpack:"v,omitempty"`
	Error   string `msgpack:"e,omitempty"`
}

type call struct {
	Endpoint string        `msgpack:"ep,omitempty"`
	Args     []interface{} `msgpack:"a,omitempty"`
	Error    string        `msgpack:"e,omitempty"`
}
