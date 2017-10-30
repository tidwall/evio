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
	wg.Add(4)
	go func() {
		testServe("tcp", ":9990", false, 10)
		wg.Done()
	}()
	go func() {
		testServe("tcp", ":9991", true, 10)
		wg.Done()
	}()
	go func() {
		testServe("tcp-net", ":9992", false, 10)
		wg.Done()
	}()
	go func() {
		testServe("tcp-net", ":9993", true, 10)
		wg.Done()
	}()
	wg.Wait()
}

func testServe(network, addr string, unix bool, nclients int) {
	var started bool
	var connected int
	var disconnected int

	var events Events
	events.Serving = func(wake func(id int) bool) (action Action) {
		return
	}
	events.Opened = func(id int, addr Addr) (out []byte, opts Options, action Action) {
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
			fmt.Printf("mismatch: %d bytes\n", len(data))
			//panic("mismatch")
		}
	}
}

func TestWake(t *testing.T) {
	testWake(":54321", false)
	testWake(":54321", true)
}
func testWake(addr string, stdlib bool) {
	var events Events
	var wake func(id int) bool
	events.Serving = func(wakefn func(id int) bool) (action Action) {
		wake = wakefn
		go func() {
			conn, err := net.Dial("tcp", ":54321")
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
	events.Opened = func(id int, addr Addr) (out []byte, opts Options, action Action) {
		cid = id
		return
	}
	events.Closed = func(id int) (action Action) {
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
				wake(cid)
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
		must(Serve(events, "tcp-net://:54321"))
	} else {
		must(Serve(events, "tcp://:54321"))
	}
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func TestTick(t *testing.T) {
	testTick(":54321", false)
	testTick(":54321", true)
}
func testTick(addr string, stdlib bool) {
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
		must(Serve(events, "tcp-net://"+addr))
	} else {
		must(Serve(events, "tcp://"+addr))
	}
	dur := time.Since(start)
	if dur < 250&time.Millisecond || dur > time.Second {
		panic("bad ticker timing")
	}
}

func TestDetach(t *testing.T) {
	testDetach(":54231", false)
	testDetach(":54231", true)
}
func testDetach(addr string, stdlib bool) {
	var events Events
	events.Data = func(id int, in []byte) (out []byte, action Action) {
		if string(in) == "detach\r\n" {
			return nil, Detach
		}
		out = in
		return
	}
	expected := "detached\r\n"
	var done int64
	events.Detached = func(id int, rwc io.ReadWriteCloser) (action Action) {
		go func() {
			n, err := rwc.Write([]byte(expected))
			must(err)
			if n != len(expected) {
				panic("not enough data written")
			}
		}()
		return
	}
	events.Serving = func(_ func(id int) bool) (action Action) {
		go func() {
			conn, err := net.Dial("tcp", addr)
			must(err)
			defer conn.Close()
			_, err = conn.Write([]byte("detach\r\n"))
			must(err)
			packet := make([]byte, len(expected))
			_, err = io.ReadFull(conn, packet)
			must(err)
			if string(packet) != string(expected) {
				must(fmt.Errorf("mismatch: expected '%s', got '%s'", expected, packet))
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
		must(Serve(events, "tcp-net://"+addr))
	} else {
		must(Serve(events, "tcp://"+addr))
	}

}
