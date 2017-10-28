package main

import (
	"log"

	"github.com/tidwall/doppio"
)

func main() {

	var events doppio.Events

	// Serving fires when the server can accept connections.
	events.Serving = func(wake func(id int) bool) (action doppio.Action) {
		log.Print("echo server started on port 5000")
		return
	}

	// // Accept a new client connection for the specified ID.
	// events.Opened = func(id int, addr string) (out []byte, opts doppio.Options, action doppio.Action) {
	// 	// This is a good place to create a user-defined connection context.
	// 	out = []byte("Welcome to the echo server!\r\n" +
	// 		"Enter 'quit' to close your connection or " +
	// 		"'shutdown' to close the server.\r\n")
	// 	return
	// }

	// Handle incoming data.
	events.Data = func(id int, in []byte) (out []byte, action doppio.Action) {
		out = in
		// if string(in) == "shutdown\r\n" {
		// 	action = doppio.Shutdown
		// } else if string(in) == "quit\r\n" {
		// 	action = doppio.Close
		// }
		// out = in
		return
	}

	log.Fatal(doppio.Serve(events, "tcp://0.0.0.0:5000", "unix://socket"))
}
