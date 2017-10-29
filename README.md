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
// Serve starts handling events for the specified addresses. 
// Addresses should be formatted like `tcp://192.168.0.10:9851` or `unix://socket`.
func Serve(events Events, addr ...string) error
```

The Events type is defined as:

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
- The `wake` function is there to wake up the event loop from a background goroutine. This is useful for when you need to perform a long-running operation that must send data back to a client after the operation is completed, but without blocking the server. A call to `wake` fires a `Data` event providing an opening to write data to the client. The `in` param of the `Data` event is `nil` for wakeups.
- `Data`, `Opened`, `Closed`, `Prewrite`, and `Postwrite` events have an `id` param which is a unique number assigned to the client socket.
- `in` represents an input network packet from a client, and `out` is output data sent to the client.
- The `Action` return value allows for closing or detaching a connection, or shutting down the server.


## Example - Simple echo server

```
package main

import "github.com/tidwall/evio"

func main() {
	var events evio.Events
	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		out = in
		return
	}
	if err := evio.Serve(events, "tcp://localhost:5000"); err != nil {
		println(err.Error())
	}
}
```

Connect to the server:

```
$ telnet localhost 5000
```

### Multiple addresses

You can bind to multiple address and share the same event loop.

```go
evio.Serve(events, "tcp://192.168.0.10:5000", "unix://socket")
```

## More examples

Please check out the [examples](examples) subdirectory for a simplified [redis](examples/redis-server/main.go) clone, an [echo](examples/echo-server/main.go) server, and a very basic [http](examples/http-server/main.go) server.

To run an example:

```bash
$ go run examples/http-server/main.go
```

## Performance

The benchmarks below use pipelining which allows for combining multiple Redis commands into a single packet.

**Real Redis**

```
$ redis-server
```
```
redis-benchmark -p 6379 -t ping,set,get -q -P 32
PING_INLINE: 869565.19 requests per second
PING_BULK: 1694915.25 requests per second
SET: 917431.19 requests per second
GET: 1265822.75 requests per second
```

**Redis clone (evio)**

```
$ go run examples/redis-server/main.go
```
```
redis-benchmark -p 6380 -t ping,set,get -q -P 32
PING_INLINE: 2380952.50 requests per second
PING_BULK: 2380952.50 requests per second
SET: 2325581.25 requests per second
GET: 2222222.25 requests per second
```

*Running on a MacBook Pro 15" 2.8 GHz Intel Core i7 using Go 1.7*

## Contact

Josh Baker [@tidwall](http://twitter.com/tidwall)

## License

`evio` source code is available under the MIT [License](/LICENSE).

