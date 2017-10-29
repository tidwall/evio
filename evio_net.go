package evio

import (
	"net"
	"sort"
	"sync"
	"time"
)

// servenet uses the stdlib net package instead of syscalls.
func servenet(events Events, lns []*listener) error {
	type cconn struct {
		id       int
		conn     net.Conn
		outbuf   []byte
		outpos   int
		action   Action
		wake     bool
		detached bool
	}
	type fail struct{ err error }
	type accept struct {
		lnidx int
		conn  net.Conn
	}
	type read struct {
		conn   net.Conn
		packet []byte
	}
	type tick struct{}
	type write struct{ conn net.Conn }
	type close struct{ conn net.Conn }
	var cond = sync.NewCond(&sync.Mutex{})
	lock := func() { cond.L.Lock() }
	unlock := func() { cond.L.Unlock() }
	var evs []interface{}
	send := func(ev interface{}, broadcast bool) {
		if broadcast {
			lock()
		}
		if _, ok := ev.(fail); ok {
			evs = evs[:0]
		}
		evs = append(evs, ev)
		if broadcast {
			cond.Broadcast()
			unlock()
		}
	}
	for i, ln := range lns {
		go func(lnidx int, ln net.Listener) {
			for {
				conn, err := ln.Accept()
				if err != nil {
					send(fail{err}, true)
					return
				}
				send(accept{lnidx, conn}, true)
				go func(conn net.Conn) {
					defer send(close{conn}, true)
					var packet [0xFFFF]byte
					for {
						n, err := conn.Read(packet[:])
						if err != nil && !istimeout(err) {
							return
						} else if n > 0 {
							send(read{conn, append([]byte{}, packet[:n]...)}, true)
						}
					}
				}(conn)
			}
		}(i, ln.ln)
	}
	var id int
	var connconn = make(map[net.Conn]*cconn)
	var idconn = make(map[int]*cconn)
	wake := func(id int) bool {
		var ok = true
		var err error
		lock()
		c := idconn[id]
		if c == nil {
			ok = false
		} else if !c.wake {
			c.wake = true
			send(read{c.conn, nil}, false)
			cond.Broadcast()
		}
		unlock()
		if err != nil {
			panic(err)
		}
		return ok

	}
	if events.Serving != nil {
		if events.Serving(wake) == Shutdown {
			return nil
		}
	}
	defer func() {
		lock()
		type connid struct {
			conn net.Conn
			id   int
		}
		var connids []connid
		for id, conn := range idconn {
			connids = append(connids, connid{conn.conn, id})
		}
		sort.Slice(connids, func(i, j int) bool {
			return connids[j].id < connids[i].id
		})
		for _, connid := range connids {
			connid.conn.Close()
			if events.Closed != nil {
				unlock()
				events.Closed(connid.id)
				lock()
			}
		}
		unlock()
	}()
	var tickerDelay time.Duration
	if events.Tick != nil {
		go func() {
			for {
				send(tick{}, true)
				lock()
				d := tickerDelay
				unlock()
				time.Sleep(d)
			}
		}()
	}
	lock()
again:
	for {
		for i := 0; i < len(evs); i++ {
			ev := evs[i]
			switch ev := ev.(type) {
			case nil:
			case tick:
				if events.Tick != nil {
					unlock()
					delay, action := events.Tick()
					lock()
					tickerDelay = delay
					if action == Shutdown {
						send(fail{nil}, false)
						continue again
					}

				}
			case accept:
				id++
				c := &cconn{id: id, conn: ev.conn}
				connconn[ev.conn] = c
				idconn[id] = c
				if events.Opened != nil {
					unlock()
					out, opts, action := events.Opened(id,
						Addr{ev.lnidx, ev.conn.LocalAddr(), ev.conn.RemoteAddr()})
					lock()
					if opts.TCPKeepAlive > 0 {
						if conn, ok := ev.conn.(*net.TCPConn); ok {
							if err := conn.SetKeepAlive(true); err != nil {
								send(fail{err}, false)
								continue again
							}
							if err := conn.SetKeepAlivePeriod(opts.TCPKeepAlive); err != nil {
								send(fail{err}, false)
								continue again
							}
						}
					}
					if len(out) > 0 {
						c.outbuf = append(c.outbuf, out...)
					}
					c.action = action
					if c.action != None || len(c.outbuf) > 0 {
						send(write{c.conn}, false)
					}
				}
			case read:
				var out []byte
				var in []byte
				c := connconn[ev.conn]
				if c == nil {
					continue
				}
				if c.action != None {
					send(write{c.conn}, false)
					continue
				}
				if c.wake {
					c.wake = false
				} else {
					in = ev.packet
				}
				if events.Data != nil {
					unlock()
					out, c.action = events.Data(c.id, in)
					lock()
				}
				if len(out) > 0 {
					c.outbuf = append(c.outbuf, out...)
				}
				if c.action != None || len(c.outbuf) > 0 {
					send(write{c.conn}, false)
				}
			case write:
				c := connconn[ev.conn]
				if c == nil {
					continue
				}
				if len(c.outbuf)-c.outpos > 0 {
					if events.Prewrite != nil {
						unlock()
						action := events.Prewrite(c.id, len(c.outbuf[c.outpos:]))
						lock()
						if action > c.action {
							c.action = action
						}
					}
					c.conn.SetWriteDeadline(time.Now().Add(time.Millisecond))
					n, err := c.conn.Write(c.outbuf[c.outpos:])
					if events.Postwrite != nil {
						amount := n
						if amount < 0 {
							amount = 0
						}
						unlock()
						action := events.Postwrite(c.id, amount, len(c.outbuf)-c.outpos-amount)
						lock()
						if action > c.action {
							c.action = action
						}
					}
					if err != nil {
						if c.action == Shutdown {
							send(close{c.conn}, false)
						} else if istimeout(err) {
							send(write{c.conn}, false)
						} else {
							send(close{c.conn}, false)
						}
						continue
					}
					c.outpos += n
					if len(c.outbuf)-c.outpos == 0 {
						c.outbuf = c.outbuf[:0]
						c.outpos = 0
					}
				}
				if c.action == Shutdown {
					send(close{c.conn}, false)
				} else if len(c.outbuf)-c.outpos == 0 {
					if c.action != None {
						send(close{c.conn}, false)
					}
				} else {
					send(write{c.conn}, false)
				}
			case close:
				c := connconn[ev.conn]
				if c == nil {
					continue
				}
				delete(connconn, c.conn)
				delete(idconn, c.id)
				if c.action == Detach {
					c.detached = true
					if events.Detached != nil {
						unlock()
						c.action = events.Detached(c.id, c.conn)
						lock()
						if c.action == Shutdown {
							send(fail{nil}, false)
							continue again
						}
						continue
					}
				}
				c.conn.Close()
				if events.Closed != nil {
					unlock()
					action := events.Closed(c.id)
					lock()
					if action == Shutdown {
						c.action = Shutdown
					}
				}
				if c.action == Shutdown {
					send(fail{nil}, false)
					continue again
				}
			case fail:
				unlock()
				return ev.err
			}
		}
		evs = evs[:0]
		cond.Wait()
	}
}

func istimeout(err error) bool {
	if err, ok := err.(net.Error); ok && err.Timeout() {
		return true
	}
	return false
}
