// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"log"
	"net"

	"github.com/tidwall/evio"
)

func main() {
	var events evio.Events

	events.Serving = func(wake func(id int) bool, addrs []net.Addr) (action evio.Action) {
		log.Print("echo server started on port 5000")
		return
	}

	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		out = in
		return
	}

	log.Fatal(evio.Serve(events, "tcp://0.0.0.0:5000"))
}
