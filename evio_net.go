// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package evio

import (
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type netConn struct {
	id       int
	wake     int64
	conn     net.Conn
	detached bool
	outbuf   []byte
	err      error
}

func (c *netConn) Read(p []byte) (n int, err error) {
	return c.conn.Read(p)
}
func (c *netConn) Write(p []byte) (n int, err error) {
	if c.detached {
		if len(c.outbuf) > 0 {
			for len(c.outbuf) > 0 {
				n, err = c.conn.Write(c.outbuf)
				if n > 0 {
					c.outbuf = c.outbuf[n:]
				}
				if err != nil {
					return 0, err
				}
			}
			c.outbuf = nil
		}
		var tn int
		if len(p) > 0 {
			for len(p) > 0 {
				n, err = c.conn.Write(p)
				if n > 0 {
					p = p[n:]
					tn += n
				}
				if err != nil {
					return tn, err
				}
			}
			p = nil
		}
		return tn, nil
	}
	return c.conn.Write(p)
}
func (c *netConn) Close() error {
	return c.conn.Close()
}

// servenet uses the stdlib net package instead of syscalls.
func servenet(events Events, lns []*listener) error {
	var id int64
	var mu sync.Mutex
	var cmu sync.Mutex
	var idconn = make(map[int]*netConn)
	var done bool
	ctx := Context{
		Wake: func(id int) bool {
			cmu.Lock()
			c := idconn[id]
			cmu.Unlock()
			if c == nil {
				return false
			}
			atomic.StoreInt64(&c.wake, 1)
			// force a quick wakeup
			c.conn.SetDeadline(time.Time{}.Add(1))
			return true
		},
	}
	var swg sync.WaitGroup
	swg.Add(1)
	var ferr error
	shutdown := func(err error) {
		mu.Lock()
		if done {
			mu.Unlock()
			return
		}
		defer swg.Done()
		done = true
		ferr = err
		for _, ln := range lns {
			ln.ln.Close()
		}
		type connid struct {
			conn net.Conn
			id   int
		}
		var connids []connid
		cmu.Lock()
		for id, conn := range idconn {
			connids = append(connids, connid{conn.conn, id})
		}
		idconn = make(map[int]*netConn)
		cmu.Unlock()
		mu.Unlock()
		sort.Slice(connids, func(i, j int) bool {
			return connids[j].id < connids[i].id
		})
		for _, connid := range connids {
			connid.conn.Close()
			if events.Closed != nil {
				mu.Lock()
				events.Closed(connid.id, nil)
				mu.Unlock()
			}
		}
	}
	ctx.Addrs = make([]net.Addr, len(lns))
	for i, ln := range lns {
		ctx.Addrs[i] = ln.naddr
	}
	if events.Serving != nil {
		if events.Serving(ctx) == Shutdown {
			return nil
		}
	}
	var lwg sync.WaitGroup
	lwg.Add(len(lns))
	for i, ln := range lns {
		go func(lnidx int, ln net.Listener) {
			defer lwg.Done()
			for {
				conn, err := ln.Accept()
				if err != nil {
					if err == io.EOF {
						shutdown(nil)
					} else {
						shutdown(err)
					}
					return
				}
				id := int(atomic.AddInt64(&id, 1))
				go func(id int, conn net.Conn) {
					var closed bool
					defer func() {
						if !closed {
							conn.Close()
						}
					}()
					var packet [0xFFFF]byte
					var cout []byte
					var caction Action
					c := &netConn{id: id, conn: conn}
					cmu.Lock()
					idconn[id] = c
					cmu.Unlock()
					if events.Opened != nil {
						var out []byte
						var opts Options
						var action Action
						mu.Lock()
						if !done {
							out, opts, action = events.Opened(id, Addr{lnidx, conn.LocalAddr(), conn.RemoteAddr()})
						}
						mu.Unlock()
						if opts.TCPKeepAlive > 0 {
							if conn, ok := conn.(*net.TCPConn); ok {
								conn.SetKeepAlive(true)
								conn.SetKeepAlivePeriod(opts.TCPKeepAlive)
							}
						}
						if len(out) > 0 {
							cout = append(cout, out...)
						}
						caction = action
					}
					for {
						var n int
						var out []byte
						var action Action
						if caction != None {
							goto write
						}
						conn.SetReadDeadline(time.Now().Add(time.Microsecond))
						n, err = c.Read(packet[:])
						if err != nil && !istimeout(err) {
							if err != io.EOF {
								c.err = err
							}
							goto close
						}
						if n > 0 {
							if events.Data != nil {
								mu.Lock()
								if !done {
									out, action = events.Data(id, append([]byte{}, packet[:n]...))
								}
								mu.Unlock()
							}
						} else if atomic.LoadInt64(&c.wake) != 0 {
							atomic.StoreInt64(&c.wake, 0)
							if events.Data != nil {
								mu.Lock()
								if !done {
									out, action = events.Data(id, nil)
								}
								mu.Unlock()
							}
						}
						if len(out) > 0 {
							cout = append(cout, out...)
						}
						caction = action
						goto write
					write:
						if len(cout) > 0 {
							if events.Prewrite != nil {
								mu.Lock()
								if !done {
									action = events.Prewrite(id, len(cout))
								}
								mu.Unlock()
								if action == Shutdown {
									caction = Shutdown
								}
							}
							conn.SetWriteDeadline(time.Now().Add(time.Microsecond))
							n, err := c.Write(cout)
							if err != nil && !istimeout(err) {
								if err != io.EOF {
									c.err = err
								}
								goto close
							}
							cout = cout[n:]
							if len(cout) == 0 {
								cout = nil
							}
							if events.Postwrite != nil {
								mu.Lock()
								if !done {
									action = events.Postwrite(id, n, len(cout))
								}
								mu.Unlock()
								if action == Shutdown {
									caction = Shutdown
								}
							}
						}
						if caction == Shutdown {
							goto close
						}
						if len(cout) == 0 {
							if caction != None {
								goto close
							}
						}
						continue
					close:
						cmu.Lock()
						delete(idconn, c.id)
						cmu.Unlock()
						mu.Lock()
						if done {
							mu.Unlock()
							return
						}
						mu.Unlock()
						if caction == Detach {
							if events.Detached != nil {
								if len(cout) > 0 {
									c.outbuf = cout
								}
								c.detached = true
								conn.SetDeadline(time.Time{})
								mu.Lock()
								if !done {
									caction = events.Detached(c.id, c)
								}
								mu.Unlock()
								closed = true
								if caction == Shutdown {
									goto fail
								}
								return
							}
						}
						conn.Close()
						if events.Closed != nil {
							var action Action
							mu.Lock()
							if !done {
								action = events.Closed(c.id, c.err)
							}
							mu.Unlock()
							if action == Shutdown {
								caction = Shutdown
							}
						}
						closed = true
						if caction == Shutdown {
							goto fail
						}
						return
					fail:
						shutdown(nil)
						return
					}

				}(id, conn)
			}
		}(i, ln.ln)
	}
	go func() {
		for {
			mu.Lock()
			if done {
				mu.Unlock()
				break
			}
			mu.Unlock()
			var delay time.Duration
			var action Action
			mu.Lock()
			if events.Tick != nil {
				if !done {
					delay, action = events.Tick()
				}
			} else {
				mu.Unlock()
				break
			}
			mu.Unlock()
			if action == Shutdown {
				shutdown(nil)
				return
			}
			time.Sleep(delay)
		}
	}()
	lwg.Wait() // wait for listeners
	swg.Wait() // wait for shutdown
	return ferr
}

func istimeout(err error) bool {
	if err, ok := err.(net.Error); ok && err.Timeout() {
		return true
	}
	return false
}
