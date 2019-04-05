package msgprpc_test

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/OneOfOne/msgprpc"
)

type X struct {
	A string
}

func TestServer(t *testing.T) {
	log.SetFlags(log.Lshortfile)
	msgprpc.RegisterType(0, (*X)(nil))
	s := msgprpc.NewServer(context.Background())
	s.Listen(":9851")
	err := s.ListenTLS(":9852", "certs/server.pem", "certs/server.key")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	time.Sleep(10 * time.Millisecond)
	s.On("x", func(ctx context.Context, args ...interface{}) ([]interface{}, error) {
		t.Logf("args: %#+v %T", args, args[0])
		if args[0].(int8) == 0 {
			return nil, errors.New("err")
		}
		return args, nil
	})
	//c, err := msgprpc.NewClient("tls://localhost:9852")
	c, err := msgprpc.NewClient("tls+insecure://localhost:9852")
	if err != nil {
		t.Fatal(err)
	}
	ret, err := c.Call(context.Background(), "x", int8(1), uint64(1), float64(1), "test", []byte("x"), &X{"HI"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ret: %#+v", ret)
	ret, err = c.Call(context.Background(), "x", int8(0))
	if err == nil {
		t.Fatal("no error")
	}
	t.Logf("ret: %#+v", ret)
	c.Close()
	ret, err = c.Call(context.Background(), "x", int8(0))
	if err == nil {
		t.Fatal("no error")
	}
	t.Logf("ret: %#+v %#v", ret, err)

	s.Close()

	ret, err = c.Call(context.Background(), "x", int8(0))
	if err != io.EOF {
		t.Fatal("unexpected connection")
	}
	t.Logf("ret: %#+v %#v", ret, err)
}
