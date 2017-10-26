package doppio

import (
	"bufio"
	"io"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDoppio(t *testing.T) {
	// start a server
	// connect 10 clients
	// each client will pipe random data for 1-3 seconds.
	// the writes to the server will be random sizes. 0KB - 1MB.
	// the server will echo back the data.
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		testServer("tcp", ":9990", false, 10)
		wg.Done()
	}()
	go func() {
		testServer("tcp", ":9991", true, 10)
		wg.Done()
	}()
	go func() {
		testServer("tcp-stdlib", ":9992", false, 10)
		wg.Done()
	}()
	go func() {
		testServer("tcp-stdlib", ":9993", true, 10)
		wg.Done()
	}()
	wg.Wait()
}
func testServer(network, addr string, unix bool, nclients int) {
	var started bool
	var connected int
	var disconnected int

	var events Events
	events.Serving = func(wake func(id int) bool) (action Action) {
		return
	}
	events.Opened = func(id int, addr string) (out []byte, opts Options, action Action) {
		connected++
		out = []byte("sweetness\r\n")
		opts.TCPKeepAlive = time.Minute * 5
		return
	}
	events.Closed = func(id int) (action Action) {
		disconnected++
		if connected == disconnected && disconnected == nclients {
			action = Shutdown
		}
		return
	}
	events.Data = func(id int, in []byte) (out []byte, action Action) {
		out = in
		return
	}
	events.Tick = func() (delay time.Duration, action Action) {
		if !started {
			for i := 0; i < nclients; i++ {
				go startClient(network, addr)
			}
			started = true
		}
		delay = time.Second / 5
		return
	}
	var err error
	if unix {
		socket := strings.Replace(addr, ":", "socket", 1)
		os.RemoveAll(socket)
		defer os.RemoveAll(socket)
		err = Serve(events, network+"://"+addr, "unix://"+socket)
	} else {
		err = Serve(events, network+"://"+addr)
	}
	if err != nil {
		panic(err)
	}
}

func startClient(network, addr string) {
	network = strings.Replace(network, "-stdlib", "", -1)
	rand.Seed(time.Now().UnixNano())
	c, err := net.Dial(network, addr)
	if err != nil {
		panic(err)
	}
	defer c.Close()
	rd := bufio.NewReader(c)
	msg, err := rd.ReadBytes('\n')
	if err != nil {
		panic(err)
	}
	if string(msg) != "sweetness\r\n" {
		panic("bad header")
	}
	duration := time.Duration((rand.Float64()*2 + 1) * float64(time.Second))
	start := time.Now()
	for time.Since(start) < duration {
		sz := rand.Int() % (1024 * 1024)
		data := make([]byte, sz)
		if _, err := rand.Read(data); err != nil {
			panic(err)
		}
		if _, err := c.Write(data); err != nil {
			panic(err)
		}
		data2 := make([]byte, sz)
		if _, err := io.ReadFull(rd, data2); err != nil {
			panic(err)
		}
		if string(data) != string(data2) {
			panic("mismatch")
		}
	}
}
