package main

import (
	"log"

	"github.com/tidwall/doppio"
)

func main() {
	var events doppio.Events

	events.Serving = func(wake func(id int) bool) (action doppio.Action) {
		log.Print("echo server started on port 5000")
		return
	}

	events.Data = func(id int, in []byte) (out []byte, action doppio.Action) {
		out = in
		return
	}

	log.Fatal(doppio.Serve(events, "tcp://0.0.0.0:5000"))
}
