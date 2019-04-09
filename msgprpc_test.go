package msgprpc

import (
	"context"
	"errors"
	"io"
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
	s.Listen(":9851")

	defer s.Close()

	if testing.Verbose() {
		s.Logger = log.New(os.Stderr, "", log.Lshortfile)
	}

	time.Sleep(10 * time.Millisecond)
	s.On("x", func(ctx context.Context, args ...interface{}) (Args, error) {
		t.Logf("args: %#+v %T", args, args[0])
		if v, ok := args[0].(int8); ok && v == 0 {
			return nil, errors.New("err")
		}
		return args, nil
	})
	//c, err := NewClient("tls://localhost:9852")
	c := NewClient("localhost:9851", "")
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

	rets, _ := c.BatchCall(context.Background(), "x", []Args{
		{"one"},
		{"two"},
		{"three"},
	})
	for _, r := range rets {
		t.Logf("%v", r)
	}
	s.Close()

	ret, err = c.Call(context.Background(), "x", int8(0))
	if err != io.EOF {
		t.Fatal("unexpected connection")
	}
	t.Logf("ret: %#+v %#v", ret, err)
}
