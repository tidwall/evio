package doppio

import (
	"errors"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Action is an action that occurs after the completion of an event
type Action int

const (
	// None indicates that no action should happen
	None Action = iota
	// Detach detaches the connection
	Detach
	// Close closes the connection
	Close
	// Shutdown shutdowns the server
	Shutdown
)

// Options are set when the connection opens
type Options struct {
	// TCPKeepAlive (SO_KEEPALIVE)
	TCPKeepAlive time.Duration
}

// Events represents server events
type Events struct {
	// Serving fires when the server can accept connections.
	// The wake parameter is a goroutine-safe function that triggers
	// a Data event (with a nil `in` parameter) for the specified id.
	Serving func(wake func(id int, out []byte) bool) (action Action)
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

type listener struct {
	ln      net.Listener
	f       *os.File
	fd      int
	network string
	addr    string
}

// Serve starts handling events for the specified addresses.
func Serve(events Events, addr ...string) error {
	if len(addr) == 0 {
		return errors.New("nothing to serve")
	}
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
		} else if !strings.Contains(addr, ":") {
			ln.network = "unix"
		}
		if strings.HasSuffix(ln.network, "-stdlib") {
			stdlib = true
			ln.network = ln.network[:len(ln.network)-7]
		}
		if ln.network == "unix" {
			os.RemoveAll(ln.addr)
		}
		var err error
		ln.ln, err = net.Listen(ln.network, ln.addr)
		if err != nil {
			return err
		}
		if !stdlib {
			if err := ln.system(); err != nil {
				return err
			}
		}
		lns = append(lns, &ln)
	}
	if stdlib {
		return servestdlib(events, lns)
	}
	return serve(events, lns)
}

type cconn struct {
	id       int
	conn     net.Conn
	outbuf   []byte
	outpos   int
	action   Action
	wake     bool
	detached bool
}

// servestdlib uses the stdlib net package instead of syscalls.
func servestdlib(events Events, lns []*listener) error {
	type fail struct{ err error }
	type accept struct{ conn net.Conn }
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
	for _, ln := range lns {
		go func(ln net.Listener) {
			for {
				conn, err := ln.Accept()
				if err != nil {
					send(fail{err}, true)
					return
				}
				send(accept{conn}, true)
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
		}(ln.ln)
	}
	var id int
	var connconn = make(map[net.Conn]*cconn)
	var idconn = make(map[int]*cconn)
	wake := func(id int, out []byte) bool {
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
					out, opts, action := events.Opened(id, ev.conn.RemoteAddr().String())
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
