// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/tidwall/evio"
)

func main() {
	var port int
	flag.IntVar(&port, "port", 5000, "server port")
	flag.Parse()

	var events evio.Events
	events.Serving = func(srv evio.Server) (action evio.Action) {
		log.Printf("echo server started on port %d", port)
		return
	}
	events.Opened = func(id int, info evio.Info) (out []byte, opts evio.Options, action evio.Action) {
		//log.Printf("opened: %d: %s", id, addr.Remote.String())
		return
	}
	events.Closed = func(id int, err error) (action evio.Action) {
		//log.Printf("closed: %d", id)
		return
	}
	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		out = in
		return
	}
	log.Fatal(evio.Serve(events, fmt.Sprintf("tcp://:%d", port)))
}
