// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/tidwall/evio"
)

func main() {
	var port int
	var udp bool
	var trace bool
	var reuseport bool
	flag.IntVar(&port, "port", 5000, "server port")
	flag.BoolVar(&udp, "udp", false, "listen on udp")
	flag.BoolVar(&reuseport, "reuseport", false, "reuseport (SO_REUSEPORT)")
	flag.BoolVar(&trace, "trace", false, "print packets to console")
	flag.Parse()

	var events evio.Events
	events.Serving = func(srv evio.Server) (action evio.Action) {
		log.Printf("echo server started on port %d", port)
		if reuseport {
			log.Printf("reuseport")
		}
		return
	}
	events.Opened = func(id int, info evio.Info) (out []byte, opts evio.Options, action evio.Action) {
		// log.Printf("opened: %d: %+v", id, info)
		return
	}
	events.Closed = func(id int, err error) (action evio.Action) {
		// log.Printf("closed: %d", id)
		return
	}
	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		log.Printf("%s", strings.TrimSpace(string(in)))
		out = in
		return
	}
	scheme := "tcp"
	if udp {
		scheme = "udp"
	}
	log.Fatal(evio.Serve(events, fmt.Sprintf("%s://:%d?reuseport=%t", scheme, port, reuseport)))
}
