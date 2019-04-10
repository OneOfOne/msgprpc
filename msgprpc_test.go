package msgprpc

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"
	"time"
)

type X struct {
	A string
}

func TestServer(t *testing.T) {
	log.SetFlags(log.Lshortfile)
	RegisterType(0, (*X)(nil))

	s := New()
	go s.ListenAndServeTLS(":9851", "./certs/server.pem", "./certs/server.key")
	defer s.Close()

	time.Sleep(time.Millisecond * 10) // wait for the server

	if testing.Verbose() {
		s.Logger = log.New(os.Stderr, "", log.Lshortfile)
	}

	time.Sleep(10 * time.Millisecond)
	s.On("x", func(ctx context.Context, args ...interface{}) ([]interface{}, error) {
		t.Logf("args: %#+v %T", args, args[0])
		if v, ok := args[0].(int8); ok && v == 0 {
			return nil, errors.New("err")
		}
		return args, nil
	})
	c := NewClient("tls+insecure://localhost:9851", "")
	ret, err := c.Call(context.Background(), "x", int8(1), uint64(1), []float64{0, 0.0000001, -0.5, -0}, "test", []byte("x"), &X{"HI"})
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
}
