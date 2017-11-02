// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/evio"
)

type request struct {
	proto, method string
	path, query   string
	head, body    string
	remoteAddr    string
}

type conn struct {
	addr evio.Addr
	is   evio.InputStream
}

func main() {
	var port int
	var tlsport int
	var tlspem string

	flag.IntVar(&port, "port", 8080, "server port")
	flag.IntVar(&tlsport, "tlsport", 4443, "tls port")
	flag.StringVar(&tlspem, "tlscert", "", "tls pem cert/key file")
	flag.Parse()

	var events evio.Events
	var conns = make(map[int]*conn)

	events.Serving = func(wakefn func(id int) bool, addr []net.Addr) (action evio.Action) {
		log.Printf("http server started on port %d", port)
		if tlspem != "" {
			log.Printf("https server started on port %d", tlsport)
		}
		return
	}

	events.Opened = func(id int, addr evio.Addr) (out []byte, opts evio.Options, action evio.Action) {
		conns[id] = &conn{addr: addr}
		log.Printf("%s: opened", addr.Remote.String())
		return
	}

	events.Closed = func(id int, err error) (action evio.Action) {
		c := conns[id]
		log.Printf("%s: closed: %v", c.addr.Remote.String(), err)
		delete(conns, id)
		return
	}

	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		if in == nil {
			return
		}
		c := conns[id]
		data := c.is.Begin(in)
		// process the pipeline
		var req request
		for {
			leftover, err := parsereq(data, &req)
			if err != nil {
				// bad thing happened
				out = appendresp(out, "500 Error", "", err.Error()+"\n")
				action = evio.Close
				break
			} else if len(leftover) == len(data) {
				// request not ready, yet
				break
			}
			// handle the request
			req.remoteAddr = c.addr.Remote.String()
			out = appendhandle(out, &req)
			data = leftover
		}
		c.is.End(data)
		return
	}
	// We at least want the single http address.
	addrs := []string{fmt.Sprintf("tcp://:%d", port)}
	if tlspem != "" {

		// load the cert and key pair from the concat'd pem file.
		cer, err := tls.LoadX509KeyPair(tlspem, tlspem)
		if err != nil {
			log.Fatal(err)
		}
		config := &tls.Config{Certificates: []tls.Certificate{cer}}
		// Update the address list to include https.
		addrs = append(addrs, fmt.Sprintf("tcp://:%d", tlsport))

		// TLS translate the events
		events = evio.Translate(events,
			func(id int, addr evio.Addr) bool {
				// only translate for the second address.
				return addr.Index == 1
			},
			func(rw io.ReadWriter) io.ReadWriter {
				// Use the standard Go crypto/tls package and create a tls.Conn
				// from the provided io.ReadWriter. Here we use the handy
				// evio.NopConn utility to create a barebone net.Conn in order
				// for the tls.Server to accept the connection.
				return tls.Server(evio.NopConn(rw), config)
			},
		)
	}
	// Start serving!
	log.Fatal(evio.Serve(events, addrs...))
}

// appendhandle handles the incoming request and appends the response to
// the provided bytes, which is then returned to the caller.
func appendhandle(b []byte, req *request) []byte {
	return appendresp(b, "200 OK", "", "Hello World!\n")
}

// appendresp will append a valid http response to the provide bytes.
// The status param should be the code plus text such as "200 OK".
// The head parameter should be a series of lines ending with "\r\n" or empty.
func appendresp(b []byte, status, head, body string) []byte {
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

// parsereq is a very simple http request parser. This operation
// waits for the entire payload to be buffered before returning a
// valid request.
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
				for i := 0; i < len(req.path); i++ {
					if req.path[i] == '?' {
						req.query = req.path[i+1:]
						req.path = req.path[:i]
						break
					}
				}
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
