package main

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/doppio"
)

type request struct {
	method, path, proto string
	head, body          string
}

type conn struct {
	id   int
	addr string
	wake func(id int) bool

	cond   *sync.Cond
	in     []byte
	out    []byte
	outi   int
	outw   int
	action doppio.Action
	closed bool
}

func (c *conn) run() {
	c.cond.L.Lock()
	for {
		if c.closed {
			break
		}
		for len(c.in) > 0 {
			in := c.in
			c.in = nil
			c.cond.L.Unlock()
			out, leftover, action := execdata(in, c.addr)
			c.cond.L.Lock()
			c.in = append(leftover, c.in...)
			if len(out) > 0 {
				c.out = append(c.out, out...)
				c.outi++
			}
			c.action = action
			if len(leftover) == len(in) {
				// nothing processed
				break
			}
		}
		if c.outi > c.outw || c.action != doppio.None {
			c.outw = c.outi
			c.wake(c.id)
		}
		c.cond.Wait()
	}
	c.cond.L.Unlock()
}

func main() {

	if false {
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			log.Fatal(err)
		}
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Fatal(err)
			}
			go func(conn net.Conn) {
				defer conn.Close()
				addr := conn.RemoteAddr().String()
				var in []byte
				var packet [0xFFFF]byte
				for {
					n, err := conn.Read(packet[:])
					if err != nil {
						return
					}
					in = append(in, packet[:n]...)
					data := in
					for {
						out, leftover, action := execdata(data, addr)
						if len(out) > 0 {
							conn.Write(out)
						}
						if action != doppio.None {
							return
						}
						if len(leftover) == len(data) {
							break
						}
						data = leftover
					}
					in = append(in[:0], data...)
				}
			}(conn)
		}
		return
	}

	var events doppio.Events
	var conns = make(map[int]*conn)
	var wake func(id int) bool

	events.Serving = func(wakefn func(id int) bool) (action doppio.Action) {
		log.Print("http server started on port 8080")
		wake = wakefn
		return
	}

	// Accept a new client connection for the specified ID.
	events.Opened = func(id int, addr string) (out []byte, opts doppio.Options, action doppio.Action) {
		c := &conn{id: id, addr: addr, cond: sync.NewCond(&sync.Mutex{}), wake: wake}
		conns[id] = c
		log.Printf("%s: opened", addr)
		go c.run()
		return
	}

	// Free resources after a connection closes
	events.Closed = func(id int) (action doppio.Action) {
		c := conns[id]
		log.Printf("%s: closed", c.addr)
		delete(conns, id)
		c.cond.L.Lock()
		c.closed = true
		c.cond.Broadcast()
		c.cond.L.Unlock()
		return
	}

	// Deal with incoming data
	events.Data = func(id int, in []byte) (out []byte, action doppio.Action) {
		c := conns[id]
		if in == nil {
			// from wakeup
			c.cond.L.Lock()
			out, action = c.out, c.action
			c.out = nil
			//println(456)
			c.cond.L.Unlock()
		} else {
			// new data
			c.cond.L.Lock()
			c.in = append(c.in, in...)
			//println(123)
			c.cond.Broadcast()
			c.cond.L.Unlock()
		}
		return
	}

	// Connect to port 8080
	log.Fatal(doppio.Serve(events, "tcp://0.0.0.0:8080"))
}

func appendhandle(b []byte, req *request, addr string) []byte {
	return appendresp(b, req, "200 OK", "", "Hello World!\n")
}

func appendresp(b []byte, req *request, status, head, body string) []byte {
	b = append(b, "HTTP/1.1"...)
	b = append(b, ' ')
	b = append(b, status...)
	b = append(b, '\r', '\n')
	b = append(b, "Date: "...)
	b = time.Now().AppendFormat(b, "Mon, 02 Jan 2006 15:04:05 GMT")
	b = append(b, '\r', '\n')
	if len(body) > 0 {
		b = append(b, "Content-Length: "...)
		b = strconv.AppendInt(b, int64(len(body)), 10)
		b = append(b, '\r', '\n')
	}
	b = append(b, head...)
	b = append(b, '\r', '\n')
	if len(body) > 0 {
		b = append(b, body...)
	}
	return b
}

func parsereq(data []byte, req *request) (leftover []byte, err error) {
	var s int
	var n int
	var top string
	var clen int
	var i int
	for ; i < len(data); i++ {
		if i > 1 && data[i] == '\n' && data[i-1] == '\r' {
			line := string(data[s : i-1])
			s = i + 1
			if n == 0 {
				top = line
				parts := strings.Split(top, " ")
				if len(parts) != 3 {
					return data, fmt.Errorf("malformed request '%s'", top)
				}
				req.method = parts[0]
				req.path = parts[1]
				req.proto = parts[2]
			} else if line == "" {
				req.head = string(data[len(top)+2 : i+1])
				i++
				if clen > 0 {
					if len(data[i:]) < clen {
						break
					}
					req.body = string(data[i : i+clen])
					i += clen
				}
				return data[i:], nil
			} else {
				if strings.HasPrefix(line, "Content-Length:") {
					n, err := strconv.ParseInt(strings.TrimSpace(line[len("Content-Length:"):]), 10, 64)
					if err == nil {
						clen = int(n)
					}
				}
			}
			n++
		}
	}
	// not enough data
	return data, nil
}

func execdata(data []byte, addr string) (out, leftover []byte, action doppio.Action) {
	var req request
	for {
		leftover, err := parsereq(data, &req)
		if err != nil {
			// bad thing happened
			out = appendresp(out, &req, "500 Error", "", err.Error()+"\n")
			action = doppio.Close
			break
		} else if len(leftover) == len(data) {
			// request not ready, yet
			break
		}
		// handle the request
		out = appendhandle(out, &req, addr)
		data = leftover
	}
	return out, data, action
}
