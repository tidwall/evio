package evio

import (
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// servenet uses the stdlib net package instead of syscalls.
func servenet(events Events, lns []*listener) error {
	type cconn struct {
		id   int
		wake int64
		conn net.Conn
	}
	var id int64
	var mu sync.Mutex
	var cmu sync.Mutex
	var idconn = make(map[int]*cconn)
	var done bool
	wake := func(id int) bool {
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
	}
	var ferr error
	shutdown := func(err error) {
		mu.Lock()
		if done {
			mu.Unlock()
			return
		}
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
		idconn = make(map[int]*cconn)
		cmu.Unlock()
		mu.Unlock()
		sort.Slice(connids, func(i, j int) bool {
			return connids[j].id < connids[i].id
		})
		for _, connid := range connids {
			connid.conn.Close()
			if events.Closed != nil {
				mu.Lock()
				events.Closed(connid.id)
				mu.Unlock()
			}
		}
	}
	if events.Serving != nil {
		if events.Serving(wake) == Shutdown {
			return nil
		}
	}

	var wg sync.WaitGroup
	wg.Add(len(lns))
	for i, ln := range lns {
		go func(lnidx int, ln net.Listener) {
			defer wg.Done()
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
					c := &cconn{id: id, conn: conn}
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
						if len(cout) > 0 {
							conn.SetReadDeadline(time.Now().Add(time.Microsecond))
						} else {
							conn.SetReadDeadline(time.Now().Add(time.Second / 5))
						}
						n, err = conn.Read(packet[:])
						if err != nil && !istimeout(err) {
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
							conn.SetWriteDeadline(time.Now().Add(time.Second / 5))
							n, err := conn.Write(cout)
							if err != nil && !istimeout(err) {
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
								mu.Lock()
								if !done {
									caction = events.Detached(c.id, conn)
								}
								mu.Unlock()
								closed = true
								if caction == Shutdown {
									goto fail
								}
								continue
							}
						}
						conn.Close()
						if events.Closed != nil {
							var action Action
							mu.Lock()
							if !done {
								action = events.Closed(c.id)
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
			var delay time.Duration
			var action Action
			if events.Tick != nil {
				mu.Lock()
				delay, action = events.Tick()
				mu.Unlock()
			}
			if action == Shutdown {
				shutdown(nil)
				return
			}
			time.Sleep(delay)
		}
	}()
	wg.Wait()
	return ferr
}

func istimeout(err error) bool {
	if err, ok := err.(net.Error); ok && err.Timeout() {
		return true
	}
	return false
}
