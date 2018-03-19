// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package evio

import (
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/kavu/go_reuseport"
)

// Action is an action that occurs after the completion of an event.
type Action int

const (
	// None indicates that no action should occur following an event.
	None Action = iota
	// Detach detaches the client. Not available for UDP connections.
	Detach
	// Close closes the client.
	Close
	// Shutdown shutdowns the server.
	Shutdown
)

// Options are set when the client opens.
type Options struct {
	// TCPKeepAlive (SO_KEEPALIVE) socket option.
	TCPKeepAlive time.Duration
	// ReuseInputBuffer will forces the connection to share and reuse the
	// same input packet buffer with all other connections that also use
	// this option.
	// Default value is false, which means that all input data which is
	// passed to the Data event will be a uniquely copied []byte slice.
	ReuseInputBuffer bool
}

// Info represents a information about the connection
type Info struct {
	// Closing is true when the connection is about to close. Expect a Closed
	// event to fire soon.
	Closing bool
	// AddrIndex is the index of server address that was passed to the Serve call.
	AddrIndex int
	// LocalAddr is the connection's local socket address.
	LocalAddr net.Addr
	// RemoteAddr is the connection's remote peer address.
	RemoteAddr net.Addr
}

// Server represents a server context which provides information about the
// running server and has control functions for managing state.
type Server struct {
	// The addrs parameter is an array of listening addresses that align
	// with the addr strings passed to the Serve function.
	Addrs []net.Addr
	// Wake is a goroutine-safe function that triggers a Data event
	// (with a nil `in` parameter) for the specified id.  Not available for
	// UDP connections.
	Wake func(id int) (ok bool)
	// Dial is a goroutine-safe function makes a connection to an external
	// server and returns a new connection id. The new connection is added
	// to the event loop and is managed exactly the same way as all the
	// other connections. This operation only fails if the server/loop has
	// been shut down. An `id` that is not zero means the operation succeeded
	// and then there always be exactly one Opened and one Closed event
	// following this call. Look for socket errors from the Closed event.
	// Not available for UDP connections.
	Dial func(addr string, timeout time.Duration) (id int)
}

// Events represents the server events for the Serve call.
// Each event has an Action return value that is used manage the state
// of the connection and server.
type Events struct {
	// Serving fires when the server can accept connections. The server
	// parameter has information and various utilities.
	Serving func(server Server) (action Action)
	// Opened fires when a new connection has opened.
	// The info parameter has information about the connection such as
	// it's local and remote address.
	// Use the out return value to write data to the connection.
	// The opts return value is used to set connection options.
	Opened func(id int, info Info) (out []byte, opts Options, action Action)
	// Closed fires when a connection has closed.
	// The err parameter is the last known connection error.
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

// Serve starts handling events for the specified addresses.
//
// Addresses should use a scheme prefix and be formatted
// like `tcp://192.168.0.10:9851` or `unix://socket`.
// Valid network schemes:
//  tcp   - bind to both IPv4 and IPv6
//  tcp4  - IPv4
//  tcp6  - IPv6
//  udp   - bind to both IPv4 and IPv6
//  udp4  - IPv4
//  udp6  - IPv6
//  unix  - Unix Domain Socket
//
// The "tcp" network scheme is assumed when one is not specified.
func Serve(events Events, addr ...string) error {
	var lns []*listener
	defer func() {
		for _, ln := range lns {
			ln.close()
		}
	}()
	var stdlib bool
	for _, addr := range addr {
		var ln listener
		var stdlibt bool
		ln.network, ln.addr, ln.opts, stdlibt = parseAddr(addr)
		if stdlibt {
			stdlib = true
		}
		if ln.network == "unix" {
			os.RemoveAll(ln.addr)
		}
		var err error
		if ln.network == "udp" {
			if ln.opts.reusePort() {
				ln.pconn, err = reuseport.ListenPacket(ln.network, ln.addr)
			} else {
				ln.pconn, err = net.ListenPacket(ln.network, ln.addr)
			}
		} else {
			if ln.opts.reusePort() {
				ln.ln, err = reuseport.Listen(ln.network, ln.addr)
			} else {
				ln.ln, err = net.Listen(ln.network, ln.addr)
			}
		}
		if err != nil {
			return err
		}
		if ln.pconn != nil {
			ln.lnaddr = ln.pconn.LocalAddr()
		} else {
			ln.lnaddr = ln.ln.Addr()
		}
		if !stdlib {
			if err := ln.system(); err != nil {
				return err
			}
		}
		lns = append(lns, &ln)
	}
	if stdlib {
		return servenet(events, lns)
	}
	return serve(events, lns)
}

// InputStream is a helper type for managing input streams inside the
// Data event.
type InputStream struct{ b []byte }

// Begin accepts a new packet and returns a working sequence of
// unprocessed bytes.
func (is *InputStream) Begin(packet []byte) (data []byte) {
	data = packet
	if len(is.b) > 0 {
		is.b = append(is.b, data...)
		data = is.b
	}
	return data
}

// End shift the stream to match the unprocessed data.
func (is *InputStream) End(data []byte) {
	if len(data) > 0 {
		if len(data) != len(is.b) {
			is.b = append(is.b[:0], data...)
		}
	} else if len(is.b) > 0 {
		is.b = is.b[:0]
	}
}

type listener struct {
	ln      net.Listener
	lnaddr  net.Addr
	pconn   net.PacketConn
	opts    addrOpts
	f       *os.File
	fd      int
	network string
	addr    string
}

type addrOpts map[string]string

func (opts addrOpts) reusePort() bool {
	switch opts["reuseport"] {
	case "yes", "true", "1":
		return true
	}
	return false
}

func parseAddr(addr string) (network, address string, opts addrOpts, stdlib bool) {
	network = "tcp"
	address = addr
	opts = make(map[string]string)
	if strings.Contains(address, "://") {
		network = strings.Split(address, "://")[0]
		address = strings.Split(address, "://")[1]
	}
	if strings.HasSuffix(network, "-net") {
		stdlib = true
		network = network[:len(network)-4]
	}
	q := strings.Index(address, "?")
	if q != -1 {
		for _, part := range strings.Split(address[q+1:], "&") {
			kv := strings.Split(part, "=")
			if len(kv) == 2 {
				opts[kv[0]] = kv[1]
			}
		}
		address = address[:q]
	}
	return
}
