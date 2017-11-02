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

The goal of this project is to create a server framework for Go that performs on par with [Redis](http://redis.io) and [Haproxy](http://www.haproxy.org) for packet handling. My hope is to use this as a foundation for [Tile38](https://github.com/tidwall/tile38) and a future L7 proxy for Go... and a bunch of other stuff.

## Features

- Very fast single-threaded event loop design
- Simple API
- Low memory usage
- Supports tcp4, tcp6, and unix sockets
- Allows multiple network binding on the same event loop
- Flexible ticker event
- Fallback for non-epoll/kqueue operating systems by simulating events with the [net](https://golang.org/pkg/net/) package.

## Getting Started

### Installing

To start using evio, install Go and run `go get`:

```sh
$ go get -u github.com/tidwall/evio
```

This will retrieve the library.

### Usage

Starting a server is easy with `evio`. Just set up your events and pass them to the `Serve` function along with the binding address(es). Each connections receives an ID that's passed to various events to differentiate the clients. At any point you can close a client or shutdown the server by return a `Close` or `Shutdown` action from an event.

Example echo server that binds to port 5000:

```go
package main

import "github.com/tidwall/evio"

func main() {
	var events evio.Events
	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		out = in
		return
	}
	if err := evio.Serve(events, "tcp://localhost:5000"); err != nil {
		panic(err.Error())
	}
}
```

The only event being used is `Data`, which fires when the server receives input data from a client.
The exact same input data is then being returned through the output return value, which is sent back to the client. 

Connect to the echo server:

```sh
$ telnet localhost 5000
```






The Events type is defined as:

```go
// Events represents the server events for the Serve call.
// Each event has an Action return value that is used manage the state
// of the connection and server.
type Events struct {
	// Serving fires when the server can accept connections.
	// The wake parameter is a goroutine-safe function that triggers
	// a Data event (with a nil `in` parameter) for the specified id.
	// The addrs parameter is an array of listening addresses that align
	// with the addr strings passed to the Serve function.
	Serving func(wake func(id int) bool, addrs []net.Addr) (action Action)
	// Opened fires when a new connection has opened.
	// The addr parameter is the connection's local and remote addresses.
	// Use the out return value to write data to the connection.
	// The opts return value is used to set connection options.
	Opened func(id int, addr Addr) (out []byte, opts Options, action Action)
	// Opened fires when a connection has closed.
	// The err parameter is the last known connection error, usually nil.
	Closed func(id int, err error) (action Action)
	// Detached fires when a connection has been previously detached.
	// Once detached it's up to the receiver of this event to manage the
	// state of the connection. The Closed event will not be called for
	// this connection.
	// The conn parameter is a ReadWriteCloser that represents the
	// underlying socket connection. It can be freely used in goroutines
	// and should be closed when it's no longer needed.
	Detached func(id int, rwc io.ReadWriteCloser) (action Action)
	// Data fires when a connection sends the server data.
	// The in parameter is the incoming data.
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

