// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package evio

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServe(t *testing.T) {
	// start a server
	// connect 10 clients
	// each client will pipe random data for 1-3 seconds.
	// the writes to the server will be random sizes. 0KB - 1MB.
	// the server will echo back the data.
	// waits for graceful connection closing.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testServe("tcp", ":9990", false, 10)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testServe("tcp", ":9991", true, 10)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testServe("tcp-net", ":9992", false, 10)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testServe("tcp-net", ":9993", true, 10)
	}()
	wg.Wait()
}

func testServe(network, addr string, unix bool, nclients int) {
	var started bool
	var connected int
	var disconnected int

	var events Events
	events.Serving = func(srv Server) (action Action) {
		return
	}
	events.Opened = func(id int, info Info) (out []byte, opts Options, action Action) {
		connected++
		out = []byte("sweetness\r\n")
		opts.TCPKeepAlive = time.Minute * 5
		if info.LocalAddr == nil {
			panic("nil local addr")
		}
		if info.RemoteAddr == nil {
			panic("nil local addr")
		}
		return
	}
	events.Closed = func(id int, err error) (action Action) {
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
	network = strings.Replace(network, "-net", "", -1)
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
	duration := time.Duration((rand.Float64()*2+1)*float64(time.Second)) / 4
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
			fmt.Printf("mismatch: %d bytes\n", len(data))
			//panic("mismatch")
		}
	}
}

func TestWake(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testWake("tcp", ":9991", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testWake("tcp", ":9992", true)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testWake("unix", "socket1", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testWake("unix", "socket2", true)
	}()
	wg.Wait()
}
func testWake(network, addr string, stdlib bool) {
	var events Events
	var srv Server
	events.Serving = func(srvin Server) (action Action) {
		srv = srvin
		go func() {
			conn, err := net.Dial(network, addr)
			must(err)
			defer conn.Close()
			rd := bufio.NewReader(conn)
			for i := 0; i < 1000; i++ {
				line := []byte(fmt.Sprintf("msg%d\r\n", i))
				conn.Write(line)
				data, err := rd.ReadBytes('\n')
				must(err)
				if string(data) != string(line) {
					panic("msg mismatch")
				}
			}
		}()
		return
	}

	var cid int
	var cout []byte
	var cin []byte
	var cclosed bool
	var cond = sync.NewCond(&sync.Mutex{})
	events.Opened = func(id int, info Info) (out []byte, opts Options, action Action) {
		cid = id
		return
	}
	events.Closed = func(id int, err error) (action Action) {
		action = Shutdown
		cond.L.Lock()
		cclosed = true
		cond.Broadcast()
		cond.L.Unlock()
		return
	}
	go func() {
		cond.L.Lock()
		for !cclosed {
			if len(cin) > 0 {
				cout = append(cout, cin...)
				cin = nil
			}
			if len(cout) > 0 {
				srv.Wake(cid)
			}
			cond.Wait()
		}
		cond.L.Unlock()
	}()
	events.Data = func(id int, in []byte) (out []byte, action Action) {
		if in == nil {
			cond.L.Lock()
			out = cout
			cout = nil
			cond.L.Unlock()
		} else {
			cond.L.Lock()
			cin = append(cin, in...)
			cond.Broadcast()
			cond.L.Unlock()
		}
		return
	}
	if stdlib {
		must(Serve(events, network+"-net://"+addr))
	} else {
		must(Serve(events, network+"://"+addr))
	}
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func TestTick(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTick("tcp", ":9991", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTick("tcp", ":9992", true)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTick("unix", "socket1", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTick("unix", "socket2", true)
	}()
	wg.Wait()
}
func testTick(network, addr string, stdlib bool) {
	var events Events
	var count int
	start := time.Now()
	events.Tick = func() (delay time.Duration, action Action) {
		if count == 25 {
			action = Shutdown
			return
		}
		count++
		delay = time.Millisecond * 10
		return
	}
	if stdlib {
		must(Serve(events, network+"-net://"+addr))
	} else {
		must(Serve(events, network+"://"+addr))
	}
	dur := time.Since(start)
	if dur < 250&time.Millisecond || dur > time.Second {
		panic("bad ticker timing")
	}
}

func TestShutdown(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testShutdown("tcp", ":9991", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testShutdown("tcp", ":9992", true)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testShutdown("unix", "socket1", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testShutdown("unix", "socket2", true)
	}()
	wg.Wait()
}
func testShutdown(network, addr string, stdlib bool) {
	var events Events
	var count int
	var clients int64
	var N = 10
	events.Opened = func(id int, info Info) (out []byte, opts Options, action Action) {
		atomic.AddInt64(&clients, 1)
		return
	}
	events.Closed = func(id int, err error) (action Action) {
		atomic.AddInt64(&clients, -1)
		return
	}
	events.Tick = func() (delay time.Duration, action Action) {
		if count == 0 {
			// start clients
			for i := 0; i < N; i++ {
				go func() {
					conn, err := net.Dial(network, addr)
					must(err)
					defer conn.Close()
					_, err = conn.Read([]byte{0})
					if err == nil {
						panic("expected error")
					}
				}()
			}
		} else {
			if int(atomic.LoadInt64(&clients)) == N {
				action = Shutdown
			}
		}
		count++
		delay = time.Second / 20
		return
	}
	if stdlib {
		must(Serve(events, network+"-net://"+addr))
	} else {
		must(Serve(events, network+"://"+addr))
	}
	if clients != 0 {
		panic("did not call close on all clients")
	}
}

func TestDetach(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testDetach("tcp", ":9991", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testDetach("tcp", ":9992", true)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testDetach("unix", "socket1", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testDetach("unix", "socket2", true)
	}()
	wg.Wait()
}

func testDetach(network, addr string, stdlib bool) {
	// we will write a bunch of data with the text "--detached--" in the
	// middle followed by a bunch of data.
	rand.Seed(time.Now().UnixNano())
	rdat := make([]byte, 10*1024)
	if _, err := rand.Read(rdat); err != nil {
		panic("random error: " + err.Error())
	}
	expected := []byte(string(rdat) + "--detached--" + string(rdat))
	var cin []byte
	var events Events
	events.Data = func(id int, in []byte) (out []byte, action Action) {
		cin = append(cin, in...)
		if len(cin) == len(expected) {
			if string(cin) != string(expected) {
				panic("mismatch client -> server")
			}
			return cin, Detach
		}
		return
	}

	//expected := "detached\r\n"
	var done int64
	events.Detached = func(id int, conn io.ReadWriteCloser) (action Action) {
		go func() {
			defer conn.Close()
			// detached connection
			n, err := conn.Write([]byte(expected))
			must(err)
			if n != len(expected) {
				panic("not enough data written")
			}
		}()
		return
	}
	events.Serving = func(srv Server) (action Action) {
		go func() {
			// client connection
			conn, err := net.Dial(network, addr)
			must(err)
			defer conn.Close()
			_, err = conn.Write(expected)
			must(err)
			// read from the attached response
			packet := make([]byte, len(expected))
			time.Sleep(time.Second / 3)
			_, err = io.ReadFull(conn, packet)
			must(err)
			if string(packet) != string(expected) {
				panic("mismatch server -> client 1")
			}
			// read from the detached response
			time.Sleep(time.Second / 3)
			_, err = io.ReadFull(conn, packet)
			must(err)
			if string(packet) != string(expected) {
				panic("mismatch server -> client 2")
			}
			time.Sleep(time.Second / 3)
			_, err = conn.Read([]byte{0})

			if err == nil {
				panic("expected nil, got '" + err.Error() + "'")
			}
			atomic.StoreInt64(&done, 1)
		}()
		return
	}
	events.Tick = func() (delay time.Duration, action Action) {
		delay = time.Second / 5
		if atomic.LoadInt64(&done) == 1 {
			action = Shutdown
		}
		return
	}
	if stdlib {
		must(Serve(events, network+"-net://"+addr))
	} else {
		must(Serve(events, network+"://"+addr))
	}
}

func TestBadAddresses(t *testing.T) {
	var events Events
	events.Serving = func(srv Server) (action Action) {
		return Shutdown
	}
	if err := Serve(events, "tulip://howdy"); err == nil {
		t.Fatalf("expected error")
	}
	if err := Serve(events, "howdy"); err == nil {
		t.Fatalf("expected error")
	}
	if err := Serve(events, "tcp://"); err != nil {
		t.Fatalf("expected nil, got '%v'", err)
	}
}

func TestInputStream(t *testing.T) {
	var s InputStream
	in := []byte("HELLO")
	data := s.Begin(in)
	if string(data) != string(in) {
		t.Fatalf("expected '%v', got '%v'", in, data)
	}
	s.End(in[3:])
	data = s.Begin([]byte("WLY"))
	if string(data) != "LOWLY" {
		t.Fatalf("expected '%v', got '%v'", "LOWLY", data)
	}
	s.End(nil)
	data = s.Begin([]byte("PLAYER"))
	if string(data) != "PLAYER" {
		t.Fatalf("expected '%v', got '%v'", "PLAYER", data)
	}
}

func TestPrePostwrite(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testPrePostwrite("tcp", ":9991", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testPrePostwrite("tcp", ":9992", true)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testPrePostwrite("unix", "socket1", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testPrePostwrite("unix", "socket2", true)
	}()
	wg.Wait()
}

func testPrePostwrite(network, addr string, stdlib bool) {
	var events Events
	var srv Server
	var packets int
	var tout []byte
	events.Opened = func(id int, info Info) (out []byte, opts Options, action Action) {
		packets++
		out = []byte(fmt.Sprintf("hello %d\r\n", packets))
		tout = append(tout, out...)
		srv.Wake(id)
		return
	}
	events.Data = func(id int, in []byte) (out []byte, action Action) {
		packets++
		out = []byte(fmt.Sprintf("hello %d\r\n", packets))
		tout = append(tout, out...)
		srv.Wake(id)
		return
	}
	events.Prewrite = func(id int, amount int) (action Action) {
		if amount != len(tout) {
			panic("invalid prewrite amount")
		}
		return
	}
	events.Postwrite = func(id int, amount, remaining int) (action Action) {
		tout = tout[amount:]
		if remaining != len(tout) {
			panic("invalid postwrite amount")
		}
		return
	}
	events.Closed = func(id int, err error) (action Action) {
		action = Shutdown
		return
	}
	events.Serving = func(srvin Server) (action Action) {
		srv = srvin
		go func() {
			conn, err := net.Dial(network, addr)
			must(err)
			defer conn.Close()
			rd := bufio.NewReader(conn)
			for i := 0; i < 1000; i++ {
				line, err := rd.ReadBytes('\n')
				must(err)
				ex := fmt.Sprintf("hello %d\r\n", i+1)
				if string(line) != ex {
					panic(fmt.Sprintf("expected '%v', got '%v'", ex, line))
				}
			}
		}()
		return
	}
	if stdlib {
		must(Serve(events, network+"-net://"+addr))
	} else {
		must(Serve(events, network+"://"+addr))
	}
}

func TestTranslate(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTranslate("tcp", ":9991", "passthrough", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTranslate("tcp", ":9992", "passthrough", true)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTranslate("unix", "socket1", "passthrough", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTranslate("unix", "socket2", "passthrough", true)
	}()
	wg.Wait()
}

func testTranslate(network, addr string, kind string, stdlib bool) {
	var events Events
	events.Data = func(id int, in []byte) (out []byte, action Action) {
		out = in
		return
	}
	events.Closed = func(id int, err error) (action Action) {
		action = Shutdown
		return
	}
	events.Opened = func(id int, info Info) (out []byte, opts Options, action Action) {
		out = []byte("sweetness\r\n")
		return
	}
	events.Serving = func(srv Server) (action Action) {
		go func() {
			conn, err := net.Dial(network, addr)
			must(err)
			defer conn.Close()
			line := "sweetness\r\n"
			packet := make([]byte, len(line))
			n, err := io.ReadFull(conn, packet)
			must(err)
			if n != len(line) {
				panic("invalid amount")
			}
			if string(packet) != string(line) {
				panic(fmt.Sprintf("expected '%v', got '%v'\n", line, packet))
			}
			for i := 0; i < 100; i++ {
				line := fmt.Sprintf("hello %d\r\n", i)
				n, err := conn.Write([]byte(line))
				must(err)
				if n != len(line) {
					panic("invalid amount")
				}
				packet := make([]byte, len(line))
				n, err = io.ReadFull(conn, packet)
				must(err)
				if n != len(line) {
					panic("invalid amount")
				}
				if string(packet) != string(line) {
					panic(fmt.Sprintf("expected '%v', got '%v'\n", line, packet))
				}
			}
		}()
		return
	}

	tevents := Translate(events,
		func(id int, info Info) bool {
			return true
		},
		func(id int, rw io.ReadWriter) io.ReadWriter {
			switch kind {
			case "passthrough":
				return rw
			}
			panic("invalid kind")
		},
	)

	if stdlib {
		must(Serve(tevents, network+"-net://"+addr))
	} else {
		must(Serve(tevents, network+"://"+addr))
	}

	// test with no shoulds
	tevents = Translate(events,
		func(id int, info Info) bool {
			return false
		},
		func(id int, rw io.ReadWriter) io.ReadWriter {
			return rw
		},
	)
	if stdlib {
		must(Serve(tevents, network+"-net://"+addr))
	} else {
		must(Serve(tevents, network+"://"+addr))
	}
}

// func TestVariousAddr(t *testing.T) {
// 	var events Events
// 	var kind string
// 	events.Serving = func(wake func(id int) bool, addrs []net.Addr) (action Action) {
// 		addr := addrs[0].(*net.TCPAddr)
// 		if (kind == "tcp4" && len(addr.IP) != 4) || (kind == "tcp6" && len(addr.IP) != 16) {
// 			println(len(addr.IP))
// 			panic("invalid ip")
// 		}
// 		go func(kind string) {
// 			conn, err := net.Dial(kind, ":9991")
// 			must(err)
// 			defer conn.Close()
// 		}(kind)
// 		return
// 	}
// 	events.Closed = func(id int, err error) (action Action) {
// 		return Shutdown
// 	}
// 	kind = "tcp4"
// 	must(Serve(events, "tcp4://:9991"))
// 	kind = "tcp6"
// 	must(Serve(events, "tcp6://:9991"))
// }

func TestReuseInputBuffer(t *testing.T) {
	reuses := []bool{true, false}
	for _, reuse := range reuses {
		var events Events
		events.Opened = func(id int, info Info) (out []byte, opts Options, action Action) {
			opts.ReuseInputBuffer = reuse
			return
		}
		var prev []byte
		events.Data = func(id int, in []byte) (out []byte, action Action) {
			if prev == nil {
				prev = in
			} else {
				reused := string(in) == string(prev)
				if reused != reuse {
					t.Fatalf("expected %v, got %v", reuse, reused)
				}
				action = Shutdown
			}
			return
		}
		events.Serving = func(_ Server) (action Action) {
			go func() {
				c, err := net.Dial("tcp", ":9991")
				must(err)
				defer c.Close()
				c.Write([]byte("packet1"))
				time.Sleep(time.Second / 5)
				c.Write([]byte("packet2"))
			}()
			return
		}
		must(Serve(events, "tcp://:9991"))
	}

}
