<p align="center">
<img 
    src="logo.png" 
    width="213" height="75" border="0" alt="evio">
<br>
<a href="https://travis-ci.org/tidwall/evio"><img src="https://img.shields.io/travis/tidwall/evio.svg?style=flat-square" alt="Build Status"></a>
<a href="https://godoc.org/github.com/tidwall/evio"><img src="https://img.shields.io/badge/api-reference-blue.svg?style=flat-square" alt="GoDoc"></a>
</p>
<p align="center">Event Networking for Go</a></p>

`evio` is an event driven networking framework that is fast and small. It makes direct [epoll](https://en.wikipedia.org/wiki/Epoll) and [kqueue](https://en.wikipedia.org/wiki/Kqueue) syscalls rather than the standard Go [net](https://golang.org/pkg/net/) package. It works in a similar manner as [libuv](https://github.com/libuv/libuv) and [libevent](https://github.com/libevent/libevent).

The goal of this project is to create a server framework for Go that performs on par with [Redis](http://redis.io) and [Haproxy](http://www.haproxy.org) for packet handling, but without having to interop with Cgo. My hope is to use this as a foundation for [Tile38](https://github.com/tidwall/tile38) and other projects.

## Features

- Very fast single-threaded design
- Simple API. Only one entrypoint and eight events
- Low memory usage
- Supports tcp4, tcp6, and unix sockets
- Allows multiple network binding on the same event loop
- Has a flexible ticker event
- Support for non-epoll/kqueue operating systems by simulating events with the net package.

## Getting Started

### Installing

To start using evio, install Go and run `go get`:

```sh
$ go get -u github.com/tidwall/evio
```

This will retrieve the library.

### Usage

There's only one function:

```go
func Serve(events Events, addr ...string) error
```

The Events type has the following events:

```go
// Events represents server events
type Events struct {
	// Serving fires when the server can accept connections.
	// The wake parameter is a goroutine-safe function that triggers
	// a Data event (with a nil `in` parameter) for the specified id.
	Serving func(wake func(id int) bool) (action Action)
	// Opened fires when a new connection has opened.
	// Use the out return value to write data to the connection.
	Opened func(id int, addr string) (out []byte, opts Options, action Action)
	// Opened fires when a connection is closed
	Closed func(id int) (action Action)
	// Detached fires when a connection has been previously detached.
	Detached func(id int, conn io.ReadWriteCloser) (action Action)
	// Data fires when a connection sends the server data.
	// Use the out return value to write data to the connection.
	Data func(id int, in []byte) (out []byte, action Action)
	// Prewrite fires prior to every write attempt.
	// The amount parameter is the number of bytes that will be attempted
	// to be written to the connection.
	Prewrite func(id int, amount int) (action Action)
	// Postwrite fires immediately after every write attempt.
	// The amount parameter is the number of bytes that was written to the
	// connection.
	// The remaining parameter is the number of bytes that still remain in
	// the buffer scheduled to be written.
	Postwrite func(id int, amount, remaining int) (action Action)
	// Tick fires immediately after the server starts and will fire again
	// following the duration specified by the delay return value.
	Tick func() (delay time.Duration, action Action)
}
```


- All events are executed in the same thread as the `Serve` call.
- `handle`, `accept`, and `closed` events have an `id` param which is a unique number assigned to the client socket.  
- `data` represents a network packet.  
- `ctx` is a user-defined context or nil.  
- `wake` is a function that when called will trigger the `handle` event with zero data for the specified `id`. It can be called safely from other Goroutines.
- `ticker` is an event that fires between 1 and 60 times a second, depending on the packet traffic.

## Example

Please check out the [examples](examples) subdirectory for a simplified [redis](examples/redis-server/main.go) clone and an [echo](examples/echo-server/main.go) server.

Here's a basic echo server:

```go
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/tidwall/shiny"
)

var shutdown bool
var started bool
var port int

func main() {
	flag.IntVar(&port, "port", 9999, "server port")
	flag.Parse()
	log.Fatal(shiny.Serve("tcp", fmt.Sprintf(":%d", port),
		handle, accept, closed, ticker, nil))
}

// handle - the incoming client socket data.
func handle(id int, data []byte, ctx interface{}) (send []byte, keepopen bool) {
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
}

// accept - a new client socket has opened.
// 'wake' is a function that when called will fire a 'handle' event
// for the specified ID, and is goroutine-safe.
func accept(id int, addr string, wake func(), ctx interface{}) (send []byte, keepopen bool) {
	if shutdown {
		return nil, false
	}
	// this is a good place to create a user-defined socket context.
	return []byte(
		"Welcome to the echo server!\n" +
			"Enter 'quit' to close your connection or " +
			"'shutdown' to close the server.\n"), true
}

// closed - a client socket has closed
func closed(id int, err error, ctx interface{}) {
	// teardown the socket context here
}

// ticker - a ticker that fires between 1 and 1/20 of a second
// depending on the traffic.
func ticker(ctx interface{}) (keepserving bool) {
	if shutdown {
		// do server teardown here
		return false
	}
	if !started {
		fmt.Printf("echo server started on port %d\n", port)
		started = true
	}
	// perform various non-socket io related operations here
	return true
}
```

Run the example:

```
$ go run examples/echo-server/main.go
```

Connect to the server:

```
$ telnet localhost 9999
```

## Performance

The benchmarks below use pipelining which allows for combining multiple Redis commands into a single packet.

**Redis**

```
$ redis-server --port 6379 --appendonly no
```
```
redis-benchmark -p 6379 -t ping,set,get -q -P 128
PING_INLINE: 961538.44 requests per second
PING_BULK: 1960784.38 requests per second
SET: 943396.25 requests per second
GET: 1369863.00 requests per second
```

**Shiny**

```
$ go run examples/redis-server/main.go --port 6380 --appendonly no
```
```
redis-benchmark -p 6380 -t ping,set,get -q -P 128
PING_INLINE: 3846153.75 requests per second
PING_BULK: 4166666.75 requests per second
SET: 3703703.50 requests per second
GET: 3846153.75 requests per second
```

*Running on a MacBook Pro 15" 2.8 GHz Intel Core i7 using Go 1.7*

## Contact

Josh Baker [@tidwall](http://twitter.com/tidwall)

## License

Shiny source code is available under the MIT [License](/LICENSE).

