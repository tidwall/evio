package evio

import (
	"io"
	"net"
	"sync"
	"time"
)

type tlsc struct {
	cond    [2]*sync.Cond    // stream locks
	closed  [2]bool          // init to -1. when it reaches zero we're closed
	prebuf  [2][]byte        // buffers before translation
	postbuf [2][]byte        // buffers after translate
	rd      [2]io.ReadCloser // reader pipes
	wr      [2]io.Writer     // writer pipes
	mu      sync.Mutex       // only for the error
	err     error            // the final error if any
}

func (c *tlsc) Read(p []byte) (n int, err error)  { return c.rd[0].Read(p) }
func (c *tlsc) Write(p []byte) (n int, err error) { return c.wr[1].Write(p) }

type nopConn struct{ rw io.ReadWriter }

func (c *nopConn) Read(p []byte) (n int, err error)          { return c.rw.Read(p) }
func (c *nopConn) Write(p []byte) (n int, err error)         { return c.rw.Write(p) }
func (c *nopConn) LocalAddr() net.Addr                       { return nil }
func (c *nopConn) RemoteAddr() net.Addr                      { return nil }
func (c *nopConn) SetDeadline(deadline time.Time) error      { return nil }
func (c *nopConn) SetWriteDeadline(deadline time.Time) error { return nil }
func (c *nopConn) SetReadDeadline(deadline time.Time) error  { return nil }
func (c *nopConn) Close() error                              { return nil }

func Translate(events Events, translate func(rd io.ReadWriter) io.ReadWriter) Events {
	nevents := events
	var wake func(id int) bool
	var mu sync.Mutex
	idc := make(map[int]*tlsc)
	create := func(id int) {
		mu.Lock()
		c := &tlsc{
			cond: [2]*sync.Cond{
				sync.NewCond(&sync.Mutex{}),
				sync.NewCond(&sync.Mutex{}),
			},
		}

		idc[id] = c
		mu.Unlock()
		tc := translate(c)
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
					wake(id)
				}
			}(st, wr)
		}
	}
	destroy := func(id int) error {
		mu.Lock()
		c := idc[id]
		mu.Unlock()
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
	write := func(id, st int, b []byte) {
		mu.Lock()
		c := idc[id]
		mu.Unlock()
		c.cond[st].L.Lock()
		c.prebuf[st] = append(c.prebuf[st], b...)
		c.cond[st].Broadcast()
		c.cond[st].L.Unlock()

	}
	read := func(id, st int) []byte {
		mu.Lock()
		c := idc[id]
		mu.Unlock()
		c.cond[st].L.Lock()
		buf := c.postbuf[st]
		c.postbuf[st] = nil
		c.cond[st].L.Unlock()
		return buf
	}

	nevents.Serving = func(wakefn func(id int) bool) (action Action) {
		wake = wakefn
		return events.Serving(wakefn)
	}
	nevents.Opened = func(id int, addr Addr) (out []byte, opts Options, action Action) {
		create(id)
		out, opts, action = events.Opened(id, addr)
		if len(out) > 0 {
			write(id, 1, out)
		}
		return
	}
	nevents.Closed = func(id int, err error) (action Action) {
		ferr := destroy(id)
		if err == nil {
			err = ferr
		}
		action = events.Closed(id, err)
		return
	}
	nevents.Data = func(id int, in []byte) (out []byte, action Action) {
		if in == nil {
			out = read(id, 1)
			if len(out) > 0 {
				wake(id)
				return
			}
			in = read(id, 0)
			if len(in) > 0 {
				out, action = events.Data(id, in)
				if len(out) > 0 {
					write(id, 1, out)
					out = nil
				}
				wake(id)
				return
			}
		} else if len(in) > 0 {
			write(id, 0, in)
			in = nil
		}
		return
	}
	return nevents
}
