package main

import (
	"log"

	"github.com/tidwall/evio"
)

func main() {
	var events evio.Events

	events.Serving = func(wake func(id int) bool) (action evio.Action) {
		log.Print("echo server started on port 5000")
		return
	}

	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		out = in
		return
	}

	log.Fatal(evio.Serve(events, "tcp://0.0.0.0:5000"))
}
