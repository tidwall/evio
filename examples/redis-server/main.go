// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/evio"
	"github.com/tidwall/redcon"
)

type conn struct {
	is   evio.InputStream
	addr string
	wget bool
}

func main() {
	var port int
	var unixsocket string
	var srv evio.Server
	flag.IntVar(&port, "port", 6380, "server port")
	flag.StringVar(&unixsocket, "unixsocket", "socket", "unix socket")
	flag.Parse()
	var conns = make(map[int]*conn)
	var keys = make(map[string]string)
	var events evio.Events
	events.Serving = func(srvin evio.Server) (action evio.Action) {
		srv = srvin
		log.Printf("redis server started on port %d", port)
		if unixsocket != "" {
			log.Printf("redis server started at %s", unixsocket)
		}
		return
	}
	wgetids := make(map[int]time.Time)
	events.Opened = func(id int, info evio.Info) (out []byte, opts evio.Options, action evio.Action) {
		c := &conn{}
		if !wgetids[id].IsZero() {
			delete(wgetids, id)
			c.wget = true
		}
		conns[id] = c
		println("opened", id, c.wget)
		if c.wget {
			out = []byte("GET / HTTP/1.0\r\n\r\n")
		}
		return
	}
	events.Tick = func() (delay time.Duration, action evio.Action) {
		now := time.Now()
		for id, t := range wgetids {
			if now.Sub(t) > time.Second {
				srv.Wake(id)
			}
		}
		delay = time.Second
		return
	}
	events.Closed = func(id int, err error) (action evio.Action) {
		fmt.Printf("closed %d %v\n", id, err)
		delete(conns, id)
		return
	}
	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		c := conns[id]
		if c.wget {
			print(string(in))
			return
		}
		data := c.is.Begin(in)
		var n int
		var complete bool
		var err error
		var args [][]byte
		for action == evio.None {
			complete, args, _, data, err = redcon.ReadNextCommand(data, args[:0])
			if err != nil {
				action = evio.Close
				out = redcon.AppendError(out, err.Error())
				break
			}
			if !complete {
				break
			}
			if len(args) > 0 {
				n++
				switch strings.ToUpper(string(args[0])) {
				default:
					out = redcon.AppendError(out, "ERR unknown command '"+string(args[0])+"'")
				case "WGET":
					if len(args) != 3 {
						out = redcon.AppendError(out, "ERR wrong number of arguments for '"+string(args[0])+"' command")
					} else {
						n, _ := strconv.ParseInt(string(args[2]), 10, 63)
						cid := srv.Dial("tcp://"+string(args[1]), time.Duration(n)*time.Second)
						if cid == 0 {
							out = redcon.AppendError(out, "failed to dial")
						} else {
							wgetids[cid] = time.Now()
							out = redcon.AppendOK(out)
						}
					}
				case "PING":
					if len(args) > 2 {
						out = redcon.AppendError(out, "ERR wrong number of arguments for '"+string(args[0])+"' command")
					} else if len(args) == 2 {
						out = redcon.AppendBulk(out, args[1])
					} else {
						out = redcon.AppendString(out, "PONG")
					}
				case "ECHO":
					if len(args) != 2 {
						out = redcon.AppendError(out, "ERR wrong number of arguments for '"+string(args[0])+"' command")
					} else {
						out = redcon.AppendBulk(out, args[1])
					}
				case "SHUTDOWN":
					out = redcon.AppendString(out, "OK")
					action = evio.Shutdown

				case "QUIT":
					out = redcon.AppendString(out, "OK")
					action = evio.Close
				case "GET":
					if len(args) != 2 {
						out = redcon.AppendError(out, "ERR wrong number of arguments for '"+string(args[0])+"' command")
					} else {
						val, ok := keys[string(args[1])]
						if !ok {
							out = redcon.AppendNull(out)
						} else {
							out = redcon.AppendBulkString(out, val)
						}
					}
				case "SET":
					if len(args) != 3 {
						out = redcon.AppendError(out, "ERR wrong number of arguments for '"+string(args[0])+"' command")
					} else {
						keys[string(args[1])] = string(args[2])
						out = redcon.AppendString(out, "OK")
					}
				case "DEL":
					if len(args) < 2 {
						out = redcon.AppendError(out, "ERR wrong number of arguments for '"+string(args[0])+"' command")
					} else {
						var n int
						for i := 1; i < len(args); i++ {
							if _, ok := keys[string(args[1])]; ok {
								n++
								delete(keys, string(args[1]))
							}
						}
						out = redcon.AppendInt(out, int64(n))
					}
				case "FLUSHDB":
					keys = make(map[string]string)
					out = redcon.AppendString(out, "OK")
				}
			}
		}
		c.is.End(data)
		return
	}
	addrs := []string{fmt.Sprintf("tcp://:%d", port)}
	if unixsocket != "" {
		addrs = append(addrs, fmt.Sprintf("unix://%s", unixsocket))
	}
	err := evio.Serve(events, addrs...)
	if err != nil {
		log.Fatal(err)
	}
}
