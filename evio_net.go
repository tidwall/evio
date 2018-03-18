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
	udpaddr  net.Addr
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
	type udpaddr struct {
		IP   [16]byte
		Port int
		Zone string
	}
	var idc int64
	var mu sync.Mutex
	var cmu sync.Mutex
	var idconn = make(map[int]*netConn)
	var udpconn = make(map[udpaddr]*netConn)
	var done int64
	var shutdown func(err error)

	// connloop handles an individual connection
	connloop := func(id int, conn net.Conn, lnidx int, ln net.Listener) {
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
			if atomic.LoadInt64(&done) == 0 {
				out, opts, action = events.Opened(id, Info{
					AddrIndex:  lnidx,
					LocalAddr:  conn.LocalAddr(),
					RemoteAddr: conn.RemoteAddr(),
				})
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
			var err error
			var out []byte
			var action Action
			if caction != None {
				goto write
			}
			if len(cout) > 0 || atomic.LoadInt64(&c.wake) != 0 {
				conn.SetReadDeadline(time.Now().Add(time.Microsecond))
			} else {
				conn.SetReadDeadline(time.Now().Add(time.Second))
			}
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
					if atomic.LoadInt64(&done) == 0 {
						out, action = events.Data(id, append([]byte{}, packet[:n]...))
					}
					mu.Unlock()
				}
			} else if atomic.LoadInt64(&c.wake) != 0 {
				atomic.StoreInt64(&c.wake, 0)
				if events.Data != nil {
					mu.Lock()
					if atomic.LoadInt64(&done) == 0 {
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
					if atomic.LoadInt64(&done) == 0 {
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
					if atomic.LoadInt64(&done) == 0 {
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
			if atomic.LoadInt64(&done) != 0 {
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
					if atomic.LoadInt64(&done) == 0 {
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
				if atomic.LoadInt64(&done) == 0 {
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
	}

	ctx := Server{
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
		Dial: func(addr string, timeout time.Duration) int {
			if atomic.LoadInt64(&done) != 0 {
				return 0
			}
			id := int(atomic.AddInt64(&idc, 1))
			go func() {
				network, address, _, _ := parseAddr(addr)
				var conn net.Conn
				var err error
				if timeout > 0 {
					conn, err = net.DialTimeout(network, address, timeout)
				} else {
					conn, err = net.Dial(network, address)
				}
				if err != nil {
					if events.Opened != nil {
						mu.Lock()
						_, _, action := events.Opened(id, Info{Closing: true, AddrIndex: -1})
						mu.Unlock()
						if action == Shutdown {
							shutdown(nil)
							return
						}
					}
					if events.Closed != nil {
						mu.Lock()
						action := events.Closed(id, err)
						mu.Unlock()
						if action == Shutdown {
							shutdown(nil)
							return
						}
					}
					return
				}
				go connloop(id, conn, -1, nil)
			}()
			return id
		},
	}
	var swg sync.WaitGroup
	swg.Add(1)
	var ferr error
	shutdown = func(err error) {
		mu.Lock()
		if atomic.LoadInt64(&done) != 0 {
			mu.Unlock()
			return
		}
		defer swg.Done()
		atomic.StoreInt64(&done, 1)
		ferr = err
		for _, ln := range lns {
			if ln.pconn != nil {
				ln.pconn.Close()
			} else {
				ln.ln.Close()
			}
		}
		type connid struct {
			conn net.Conn
			id   int
		}
		var connids []connid
		var udpids []int
		cmu.Lock()
		for id, conn := range idconn {
			connids = append(connids, connid{conn.conn, id})
		}
		for _, c := range udpconn {
			udpids = append(udpids, c.id)
		}
		idconn = make(map[int]*netConn)
		udpconn = make(map[udpaddr]*netConn)
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
		for _, id := range udpids {
			if events.Closed != nil {
				mu.Lock()
				events.Closed(id, nil)
				mu.Unlock()
			}
		}
	}
	ctx.Addrs = make([]net.Addr, len(lns))
	for i, ln := range lns {
		ctx.Addrs[i] = ln.lnaddr
	}
	if events.Serving != nil {
		if events.Serving(ctx) == Shutdown {
			return nil
		}
	}
	var lwg sync.WaitGroup
	lwg.Add(len(lns))
	for i, ln := range lns {
		if ln.pconn != nil {
			go func(lnidx int, pconn net.PacketConn) {
				defer lwg.Done()
				var packet [0xFFFF]byte
				for {
					n, addr, err := pconn.ReadFrom(packet[:])
					if err != nil {
						if err == io.EOF {
							shutdown(nil)
						} else {
							shutdown(err)
						}
						return
					}
					var uaddr udpaddr
					switch addr := addr.(type) {
					case *net.TCPAddr:
						copy(uaddr.IP[16-len(addr.IP):], addr.IP)
						uaddr.Zone = addr.Zone
						uaddr.Port = addr.Port
					}
					var out []byte
					var action Action
					var c *netConn
					mu.Lock()
					c = udpconn[uaddr]
					mu.Unlock()
					if c == nil {
						id := int(atomic.AddInt64(&idc, 1))
						c = &netConn{id: id, udpaddr: addr}
						mu.Lock()
						udpconn[uaddr] = c
						mu.Unlock()
						if events.Opened != nil {
							mu.Lock()
							out, _, action = events.Opened(c.id, Info{AddrIndex: lnidx, LocalAddr: pconn.LocalAddr(), RemoteAddr: addr})
							mu.Unlock()
							if len(out) > 0 {
								if events.Prewrite != nil {
									mu.Lock()
									action2 := events.Prewrite(c.id, len(out))
									mu.Unlock()
									if action2 == Shutdown {
										action = action2
									}
								}
								pconn.WriteTo(out, addr)
								if events.Prewrite != nil {
									mu.Lock()
									action2 := events.Postwrite(c.id, len(out), 0)
									mu.Unlock()
									if action2 == Shutdown {
										action = action2
									}
								}
							}
						}
					}
					if action == None {
						if events.Data != nil {
							mu.Lock()
							out, action = events.Data(c.id, append([]byte{}, packet[:n]...))
							mu.Unlock()
							if len(out) > 0 {
								if events.Prewrite != nil {
									mu.Lock()
									action2 := events.Prewrite(c.id, len(out))
									mu.Unlock()
									if action2 == Shutdown {
										action = action2
									}
								}
								pconn.WriteTo(out, addr)
								if events.Prewrite != nil {
									mu.Lock()
									action2 := events.Postwrite(c.id, len(out), 0)
									mu.Unlock()
									if action2 == Shutdown {
										action = action2
									}
								}
							}
						}
					}
					switch action {
					case Close, Detach:
						mu.Lock()
						delete(udpconn, uaddr)
						if events.Closed != nil {
							action = events.Closed(c.id, nil)
						}
						mu.Unlock()
					}
					if action == Shutdown {
						shutdown(nil)
						return
					}
				}
			}(i, ln.pconn)
		} else {
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
					id := int(atomic.AddInt64(&idc, 1))
					go connloop(id, conn, lnidx, ln)
				}
			}(i, ln.ln)
		}
	}
	go func() {
		for {
			mu.Lock()
			if atomic.LoadInt64(&done) != 0 {
				mu.Unlock()
				break
			}
			mu.Unlock()
			var delay time.Duration
			var action Action
			mu.Lock()
			if events.Tick != nil {
				if atomic.LoadInt64(&done) == 0 {
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
