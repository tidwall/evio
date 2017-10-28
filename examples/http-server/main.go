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

// type conn struct {
// 	id     int
// 	addr   string
// 	in     []byte
// 	out    []byte
// 	closed bool
// 	action doppio.Action
// 	cond   *sync.Cond
// 	wake   func(id int) bool
// }

// func (c *conn) run() {
// 	c.cond.L.Lock()
// 	for !c.closed {
// 		if len(c.in) > 0 {
// 			in := c.in
// 			c.in = nil
// 			c.cond.L.Unlock()
// 			out, leftover, action := execdata(in, c.addr)
// 			c.cond.L.Lock()
// 			c.in = append(c.in, leftover...)
// 			c.out = out
// 			c.action = action
// 		}
// 		if len(c.out) > 0 || c.action != doppio.None {
// 			c.wake(c.id)
// 		}
// 		c.cond.Wait()
// 	}
// 	c.cond.L.Unlock()
// }

// func main() {
// 	var events doppio.Events
// 	var conns = make(map[int]*conn)
// 	var wake func(id int) bool

// 	events.Serving = func(wakefn func(id int) bool) (action doppio.Action) {
// 		log.Print("http server started on port 8080")
// 		wake = wakefn
// 		return
// 	}

// 	// Accept a new client connection for the specified ID.
// 	events.Opened = func(id int, addr string) (out []byte, opts doppio.Options, action doppio.Action) {
// 		c := &conn{id: id, addr: addr, cond: sync.NewCond(&sync.Mutex{}), wake: wake}
// 		conns[id] = c
// 		log.Printf("%s: opened", addr)
// 		go c.run()
// 		return
// 	}

// 	// Free resources after a connection closes
// 	events.Closed = func(id int) (action doppio.Action) {
// 		c := conns[id]
// 		log.Printf("%s: closed", c.addr)
// 		delete(conns, id)
// 		c.cond.L.Lock()
// 		c.closed = true
// 		c.cond.Signal()
// 		c.cond.L.Unlock()
// 		return
// 	}

// 	// Deal with incoming data
// 	events.Data = func(id int, in []byte) (out []byte, action doppio.Action) {
// 		// 		return []byte(
// 		// 			`HTTP/1.1 200 OK
// 		// Date: Fri, 27 Oct 2017 21:51:10 GMT
// 		// Content-Length: 13
// 		// Content-Type: text/plain; charset=utf-8

// 		// Hello World!
// 		// `), doppio.None
// 		c := conns[id]
// 		if false {
// 			c.cond.L.Lock()
// 			if in == nil {
// 				// from wakeup
// 				out, action = c.out, c.action
// 				c.out = c.out[:0]
// 			} else {
// 				c.in = append(c.in, in...)
// 				c.cond.Signal()
// 			}
// 			c.cond.L.Unlock()
// 			return
// 		}

// 		data := append(c.in, in...)
// 		var outb bytes.Buffer
// 		for len(data) > 0 && action == doppio.None {
// 			var req *http.Request
// 			var resp *http.Response
// 			var err error
// 			if req, data, err = readreq(data); err != nil {
// 				// bad thing happened
// 				resp = makeresp(nil, 500, nil, []byte(err.Error()+"\n"))
// 				resp.Close = true
// 				action = doppio.Close
// 			} else if req == nil {
// 				// request not ready, yet
// 				break
// 			} else {
// 				// do something neat with the request
// 				req.RemoteAddr = c.addr
// 				resp = handle(req)
// 			}
// 			if resp != nil {
// 				resp.Write(&outb)
// 			}
// 		}
// 		if len(data) > 0 {
// 			c.in = append(c.in[:0], data...)
// 		} else if len(c.in) > 0 {
// 			c.in = c.in[:0]
// 		}
// 		out = outb.Bytes()
// 		return
// 	}

// 	// Connect to port 8080
// 	log.Fatal(doppio.Serve(events, "tcp://0.0.0.0:8080"))
// }

// func execdata(in []byte, addr string) (out []byte, leftover []byte, action doppio.Action) {
// 	var outb bytes.Buffer
// 	for len(in) > 0 && action == doppio.None {
// 		var req *http.Request
// 		var resp *http.Response
// 		var err error
// 		if req, in, err = readreq(in); err != nil {
// 			// bad thing happened
// 			resp = makeresp(nil, 500, nil, []byte(err.Error()+"\n"))
// 			resp.Close = true
// 			action = doppio.Close
// 		} else if req == nil {
// 			// request not ready, yet
// 			break
// 		} else {
// 			// do something neat with the request
// 			req.RemoteAddr = addr
// 			resp = handle(req)
// 		}
// 		if resp != nil {
// 			resp.Write(&outb)
// 		}
// 	}
// 	return outb.Bytes(), in, action
// }

// func readreq(data []byte) (req *http.Request, leftover []byte, err error) {
// 	if !bytes.Contains(data, []byte("\r\n\r\n")) {
// 		// request not ready, just return the original data back
// 		return nil, data, nil
// 	}
// 	buf := bytes.NewBuffer(data)
// 	rd := bufio.NewReader(buf)
// 	req, err = http.ReadRequest(rd)
// 	leftover = data[len(data)-buf.Len()-rd.Buffered():]
// 	if err != nil {
// 		return
// 	}
// 	if req.ContentLength > 0 {
// 		// read the body content
// 		if int64(len(leftover)) < req.ContentLength {
// 			// not ready, just return the original data back
// 			return nil, data, nil
// 		}
// 		req.Body = ioutil.NopCloser(bytes.NewBuffer(leftover[:int(req.ContentLength)]))
// 		leftover = leftover[int(req.ContentLength):]
// 	}
// 	return
// }

// func makeresp(req *http.Request, code int, header http.Header, body []byte) *http.Response {
// 	return &http.Response{
// 		Request: req, StatusCode: code,
// 		ProtoMajor: 1, ProtoMinor: 1,
// 		Header:        header,
// 		ContentLength: int64(len(body)),
// 		Body:          ioutil.NopCloser(bytes.NewBuffer(body)),
// 	}
// }

// func handle(req *http.Request) *http.Response {
// 	//log.Printf("%s: %s: %s", req.RemoteAddr, req.Method, req.URL.Path)
// 	var body []byte
// 	switch req.URL.Path {
// 	default:
// 		body = []byte("Hi from " + req.URL.Path + "\n")
// 	case "/time":
// 		body = []byte(time.Now().String() + "\n")
// 	case "/":
// 		body = []byte(string("Hello World!\n"))
// 	}
// 	return makeresp(req, 200, nil, body)
// }
