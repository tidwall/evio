package shiny

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"
)

func TestShiny(t *testing.T) {
	// start a server
	// connect 10 clients
	// each client will pipe random data for 1-3 seconds.
	// the writes to the server will be random sizes. 0KB - 100KB.
	// the server will echo back the data.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		testServer(t, "tcp", 9990, 10)
		wg.Done()
	}()
	go func() {
		testServer(t, "tcp-compat", 9991, 10)
		wg.Done()
	}()
	wg.Wait()
}
func testServer(t *testing.T, net string, port int, nclients int) {
	var started bool
	var connected int
	var disconnected int
	var shutdown bool
	err := Serve(net, fmt.Sprintf(":%d", port),
		func(id int, data []byte, ctx interface{}) (send []byte, keepopen bool) {
			if shutdown {
				return nil, false
			}
			return data, true
		},
		func(id int, addr string, wake func(), ctx interface{}) (send []byte, keepopen bool) {
			if shutdown {
				return nil, false
			}
			connected++
			return []byte("sweetness\n"), true
		},
		func(id int, err error, ctx interface{}) {
			if shutdown {
				return
			}
			disconnected++
			if connected == disconnected && disconnected == nclients {
				shutdown = true
			}
		},
		func(ctx interface{}) (keepserving bool) {
			if shutdown {
				return false
			}
			if !started {
				for i := 0; i < nclients; i++ {
					go startClient(t, port)
				}
				started = true
			}
			return true
		}, "coolness")
	if err != nil {
		t.Fatal(err)
	}
}

func startClient(t *testing.T, port int) {
	rand.Seed(time.Now().UnixNano())
	c, err := net.Dial("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rd := bufio.NewReader(c)
	msg, err := rd.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "sweetness\n" {
		t.Fatal("bad header")
	}
	duration := time.Duration((rand.Float64()*2 + 1) * float64(time.Second))
	start := time.Now()
	for time.Since(start) < duration {
		sz := rand.Int() % (1024 * 100)
		data := make([]byte, sz)
		if _, err := rand.Read(data); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Write(data); err != nil {
			t.Fatal(err)
		}
		data2 := make([]byte, sz)
		if _, err := io.ReadFull(rd, data2); err != nil {
			t.Fatal(err)
		}
		if string(data) != string(data2) {
			t.Fatal("mismatch")
		}
	}
}
