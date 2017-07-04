package main

import (
	"flag"
	"fmt"

	"github.com/tidwall/shiny"
)

func main() {
	var port int
	flag.IntVar(&port, "port", 9999, "server port")
	flag.Parse()

	var shutdown bool
	var started bool
	fmt.Println(shiny.Serve("tcp", fmt.Sprintf(":%d", port),
		// handle - the incoming client socket data.
		func(id int, data []byte, ctx interface{}) (send []byte, keepopen bool) {
			if shutdown {
				return nil, false
			}
			keepopen = true
			if string(data) == "shutdown\r\n" {
				shutdown = true
			} else if string(data) == "quit\r\n" {
				keepopen = false
			}
			return data, keepopen
		},
		// accept - a new client socket has opened.
		// 'wake' is a function that when called will fire a 'handle' event
		// for the specified ID, and is goroutine-safe.
		func(id int, addr string, wake func(), ctx interface{}) (send []byte, keepopen bool) {
			if shutdown {
				return nil, false
			}
			// this is a good place to create a user-defined socket context.
			return []byte(
				"Welcome to the echo server!\n" +
					"Enter 'quit' to close your connection or " +
					"'shutdown' to close the server.\n"), true
		},
		// closed - a client socket has closed
		func(id int, err error, ctx interface{}) {
			// teardown the socket context here
		},
		// ticker - a ticker that fires between 1 and 1/20 of a second
		// depending on the traffic.
		func(ctx interface{}) (keepserveropen bool) {
			if shutdown {
				// do server teardown here
				return false
			}
			if !started {
				fmt.Printf("echo server started on port %d\n", port)
				started = true
			}
			// perform various non-socket-io related operation here
			return true
		},
		// an optional user-defined context
		nil))
}
