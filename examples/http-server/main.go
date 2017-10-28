package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/doppio"
)

type request struct {
	method, path, proto string
	head, body          string
}

type conn struct {
	addr string
	in   []byte
}

func main() {
	var events doppio.Events
	var conns = make(map[int]*conn)

	events.Serving = func(wakefn func(id int) bool) (action doppio.Action) {
		log.Print("http server started on port 8080")
		return
	}

	// Accept a new client connection for the specified ID.
	events.Opened = func(id int, addr string) (out []byte, opts doppio.Options, action doppio.Action) {
		conns[id] = &conn{addr: addr}
		log.Printf("%s: opened", addr)
		return
	}

	// Free resources after a connection closes
	events.Closed = func(id int) (action doppio.Action) {
		c := conns[id]
		log.Printf("%s: closed", c.addr)
		delete(conns, id)
		return
	}

	// Deal with incoming data
	events.Data = func(id int, in []byte) (out []byte, action doppio.Action) {
		c := conns[id]
		data := in
		if len(c.in) > 0 {
			data = append(c.in, data...)
		}
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
			out = appendhandle(out, &req, c.addr)
			data = leftover
		}
		if len(data) > 0 {
			c.in = append(c.in[:0], data...)
		} else if len(c.in) > 0 {
			c.in = c.in[:0]
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
