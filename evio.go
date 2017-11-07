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
)

// Action is an action that occurs after the completion of an event.
type Action int

const (
	// None indicates that no action should occur following an event.
	None Action = iota
	// Detach detaches the client.
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
}

// Addr represents the connection's remote and local addresses.
type Addr struct {
	// Index is the index of server address that was passed to the Serve call.
	Index int
	// Local is the connection's local socket address.
	Local net.Addr
	// Local is the connection's remote peer address.
	Remote net.Addr
}

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
	// Closed fires when a connection has closed.
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

// Serve starts handling events for the specified addresses.
//
// Addresses should use a scheme prefix and be formatted
// like `tcp://192.168.0.10:9851` or `unix://socket`.
// Valid network schemes:
//	tcp   - bind to both IPv4 and IPv6
//  tcp4  - IPv4
//  tcp6  - IPv6
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
		ln := listener{network: "tcp", addr: addr}
		if strings.Contains(addr, "://") {
			ln.network = strings.Split(addr, "://")[0]
			ln.addr = strings.Split(addr, "://")[1]
		}
		if strings.HasSuffix(ln.network, "-net") {
			stdlib = true
			ln.network = ln.network[:len(ln.network)-4]
		}
		if ln.network == "unix" {
			os.RemoveAll(ln.addr)
		}
		var err error
		ln.ln, err = net.Listen(ln.network, ln.addr)
		if err != nil {
			return err
		}
		ln.naddr = ln.ln.Addr()
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
	f       *os.File
	fd      int
	network string
	addr    string
	naddr   net.Addr
}
