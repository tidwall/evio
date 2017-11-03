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
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/evio"
)

var res string

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
	var aaaa bool
	var noparse bool

	flag.IntVar(&port, "port", 8080, "server port")
	flag.IntVar(&tlsport, "tlsport", 4443, "tls port")
	flag.StringVar(&tlspem, "tlscert", "", "tls pem cert/key file")
	flag.BoolVar(&aaaa, "aaaa", false, "aaaaa....")
	flag.BoolVar(&noparse, "noparse", true, "do not parse requests")
	flag.Parse()

	if os.Getenv("NOPARSE") == "1" {
		noparse = true
	}

	if aaaa {
		res = strings.Repeat("a", 1024)
	} else {
		res = "Hello World!\r\n"
	}

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
		//log.Printf("opened: %d: %s: %s", id, addr.Local.String(), addr.Remote.String())
		return
	}

	events.Closed = func(id int, err error) (action evio.Action) {
		// c := conns[id]
		// log.Printf("closed: %d: %s: %s", id, c.addr.Local.String(), c.addr.Remote.String())
		delete(conns, id)
		return
	}

	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		if in == nil {
			return
		}
		if noparse {
			// for testing minimal single packet request -> response.
			out = []byte("HTTP/1.1 200 OK\r\nServer: evio\r\nDate: Fri, 03 Nov 2017 11:37:00 GMT\r\nContent-Length: 14\r\n\r\nHello World!\r\n")
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
			func(id int, rw io.ReadWriter) io.ReadWriter {
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
	return appendresp(b, "200 OK", "", res)
}

// appendresp will append a valid http response to the provide bytes.
// The status param should be the code plus text such as "200 OK".
// The head parameter should be a series of lines ending with "\r\n" or empty.
func appendresp(b []byte, status, head, body string) []byte {
	b = append(b, "HTTP/1.1"...)
	b = append(b, ' ')
	b = append(b, status...)
	b = append(b, '\r', '\n')
	b = append(b, "Server: evio\r\n"...)
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
	sdata := string(data)
	var i, s int
	var top string
	var clen int
	var q = -1
	// method, path, proto line
	for ; i < len(sdata); i++ {
		if sdata[i] == ' ' {
			req.method = sdata[s:i]
			for i, s = i+1, i+1; i < len(sdata); i++ {
				if sdata[i] == '?' && q == -1 {
					q = i - s
				} else if sdata[i] == ' ' {
					if q != -1 {
						req.path = sdata[s:q]
						req.query = req.path[q+1 : i]
					} else {
						req.path = sdata[s:i]
					}
					for i, s = i+1, i+1; i < len(sdata); i++ {
						if sdata[i] == '\n' && sdata[i-1] == '\r' {
							req.proto = sdata[s:i]
							i, s = i+1, i+1
							break
						}
					}
					break
				}
			}
			break
		}
	}
	if req.proto == "" {
		return data, fmt.Errorf("malformed request")
	}
	top = sdata[:s]
	for ; i < len(sdata); i++ {
		if i > 1 && sdata[i] == '\n' && sdata[i-1] == '\r' {
			line := sdata[s : i-1]
			s = i + 1
			if line == "" {
				req.head = sdata[len(top)+2 : i+1]
				i++
				if clen > 0 {
					if len(sdata[i:]) < clen {
						break
					}
					req.body = sdata[i : i+clen]
					i += clen
				}
				return data[i:], nil
			}
			if strings.HasPrefix(line, "Content-Length:") {
				n, err := strconv.ParseInt(strings.TrimSpace(line[len("Content-Length:"):]), 10, 64)
				if err == nil {
					clen = int(n)
				}
			}
		}
	}
	// not enough data
	return data, nil
}
