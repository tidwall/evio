// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package evio

import (
	"io"
	"net"
	"sync"
	"time"
)

type tconn struct {
	cond    [2]*sync.Cond    // stream locks
	closed  [2]bool          // init to -1. when it reaches zero we're closed
	prebuf  [2][]byte        // buffers before translation
	postbuf [2][]byte        // buffers after translate
	rd      [2]io.ReadCloser // reader pipes
	wr      [2]io.Writer     // writer pipes
	mu      sync.Mutex       // only for the error
	action  Action           // the last known action
	err     error            // the final error if any
}

func (c *tconn) write(st int, b []byte) {
	c.cond[st].L.Lock()
	c.prebuf[st] = append(c.prebuf[st], b...)
	c.cond[st].Broadcast()
	c.cond[st].L.Unlock()
}

func (c *tconn) read(st int) []byte {
	c.cond[st].L.Lock()
	buf := c.postbuf[st]
	c.postbuf[st] = nil
	c.cond[st].L.Unlock()
	return buf
}

func (c *tconn) Read(p []byte) (n int, err error)  { return c.rd[0].Read(p) }
func (c *tconn) Write(p []byte) (n int, err error) { return c.wr[1].Write(p) }

// nopConn just wraps a io.ReadWriter and makes it into a net.Conn.
type nopConn struct{ io.ReadWriter }

func (c *nopConn) Read(p []byte) (n int, err error)          { return c.ReadWriter.Read(p) }
func (c *nopConn) Write(p []byte) (n int, err error)         { return c.ReadWriter.Write(p) }
func (c *nopConn) LocalAddr() net.Addr                       { return nil }
func (c *nopConn) RemoteAddr() net.Addr                      { return nil }
func (c *nopConn) SetDeadline(deadline time.Time) error      { return nil }
func (c *nopConn) SetWriteDeadline(deadline time.Time) error { return nil }
func (c *nopConn) SetReadDeadline(deadline time.Time) error  { return nil }
func (c *nopConn) Close() error                              { return nil }

// NopConn returns a net.Conn with a no-op LocalAddr, RemoteAddr,
// SetDeadline, SetWriteDeadline, SetReadDeadline, and Close methods wrapping
// the provided ReadWriter rw.
func NopConn(rw io.ReadWriter) net.Conn {
	return &nopConn{rw}
}

// Translate provides a utility for performing byte level translation on the
// input and output streams for a connection. This is useful for things like
// compression, encryption, TLS, etc. The function wraps existing events and
// returns new events that manage the translation. The `should` parameter is
// an optional function that can be used to ignore or accept the translation
// for a specific connection. The `translate` parameter is a function that
// provides a ReadWriter for each new connection and returns a ReadWriter
// that performs the actual translation.
func Translate(
	events Events,
	should func(id int, info Info) bool,
	translate func(id int, rd io.ReadWriter) io.ReadWriter,
) Events {
	tevents := events
	var ctx Server
	var mu sync.Mutex
	idc := make(map[int]*tconn)
	get := func(id int) *tconn {
		mu.Lock()
		c := idc[id]
		mu.Unlock()
		return c
	}
	create := func(id int) *tconn {
		mu.Lock()
		c := &tconn{
			cond: [2]*sync.Cond{
				sync.NewCond(&sync.Mutex{}),
				sync.NewCond(&sync.Mutex{}),
			},
		}
		idc[id] = c
		mu.Unlock()
		tc := translate(id, c)
		for st := 0; st < 2; st++ {
			c.rd[st], c.wr[st] = io.Pipe()
			var rd io.Reader
			var wr io.Writer
			if st == 0 {
				rd = tc
				wr = c.wr[0]
			} else {
				rd = c.rd[1]
				wr = tc
			}
			go func(st int, rd io.Reader, wr io.Writer) {
				c.cond[st].L.Lock()
				for {
					if c.closed[st] {
						break
					}
					if len(c.prebuf[st]) > 0 {
						buf := c.prebuf[st]
						c.prebuf[st] = nil
						c.cond[st].L.Unlock()
						n, err := wr.Write(buf)
						if err != nil {
							return
						}
						c.cond[st].L.Lock()
						if n > 0 {
							c.prebuf[st] = append(buf[n:], c.prebuf[st]...)
						}
						continue
					}
					c.cond[st].Wait()
				}
				c.cond[st].L.Unlock()
			}(st, rd, wr)
			go func(st int, wr io.Writer) {
				var ferr error
				defer func() {
					if ferr != nil {
						c.mu.Lock()
						if c.err == nil {
							c.err = ferr
						}
						c.mu.Unlock()
					}
				}()
				var packet [2048]byte
				for {
					n, err := rd.Read(packet[:])
					if err != nil {
						if err != io.EOF && err != io.ErrClosedPipe {
							ferr = err
						}
						return
					}
					c.cond[st].L.Lock()
					c.postbuf[st] = append(c.postbuf[st], packet[:n]...)
					c.cond[st].L.Unlock()
					ctx.Wake(id)
				}
			}(st, wr)
		}
		return c
	}

	destroy := func(c *tconn, id int) error {
		for st := 0; st < 2; st++ {
			if rd, ok := c.rd[st].(io.Closer); ok {
				rd.Close()
			}
			if wr, ok := c.wr[st].(io.Closer); ok {
				wr.Close()
			}
			c.cond[st].L.Lock()
			c.closed[st] = true
			c.cond[st].Broadcast()
			c.cond[st].L.Unlock()
		}
		mu.Lock()
		delete(idc, id)
		mu.Unlock()
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		return err
	}
	tevents.Serving = func(ctxin Server) (action Action) {
		ctx = ctxin
		if events.Serving != nil {
			action = events.Serving(ctx)
		}
		return
	}
	tevents.Opened = func(id int, info Info) (out []byte, opts Options, action Action) {
		if should != nil && !should(id, info) {
			if events.Opened != nil {
				out, opts, action = events.Opened(id, info)
			}
			return
		}
		c := create(id)
		if events.Opened != nil {
			out, opts, c.action = events.Opened(id, info)
			if len(out) > 0 {
				c.write(1, out)
				out = nil
				ctx.Wake(id)
			}
		}
		return
	}
	tevents.Closed = func(id int, err error) (action Action) {
		c := get(id)
		if c != nil {
			ferr := destroy(c, id)
			if err == nil {
				err = ferr
			}
		}
		if events.Closed != nil {
			action = events.Closed(id, err)
		}
		return
	}
	tevents.Data = func(id int, in []byte) (out []byte, action Action) {
		c := get(id)
		if c == nil {
			if events.Data != nil {
				out, action = events.Data(id, in)
			}
			return
		}
		if in == nil {
			// wake up
			out = c.read(1)
			if len(out) > 0 {
				ctx.Wake(id)
				return
			}
			if c.action != None {
				return nil, c.action
			}
			in = c.read(0)
			if len(in) > 0 {
				if events.Data != nil {
					out, c.action = events.Data(id, in)
					if len(out) > 0 {
						c.write(1, out)
						out = nil
					}
					ctx.Wake(id)
				}
				return
			}
		} else if len(in) > 0 {
			if c.action != None {
				return nil, c.action
			}
			// accept new input data
			c.write(0, in)
			in = nil
		}
		return
	}
	return tevents
}
