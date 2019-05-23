// Copyright 2018 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package evio

import (
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var errClosing = errors.New("closing")
var errCloseConns = errors.New("close conns")

type stdserver struct {
	events   Events         // user events
	loops    []*stdloop     // all the loops
	ln       *listener      // all the listeners
	loopwg   sync.WaitGroup // loop close waitgroup
	lnwg     sync.WaitGroup // listener close waitgroup
	cond     *sync.Cond     // shutdown signaler
	serr     error          // signal error
	accepted uintptr        // accept counter
}

type stdloop struct {
	idx   int               // loop index
	ch    chan interface{}  // command channel
	conns map[*stdconn]bool // track all the conns bound to this loop
}

type stdconn struct {
	localAddr  net.Addr
	remoteAddr net.Addr
	conn       net.Conn    // original connection
	ctx        interface{} // user-defined context
	loop       *stdloop    // owner loop
	donein     []byte      // extra data for done connection
	done       int32       // 0: attached, 1: closed
}

func (c *stdconn) Context() interface{}       { return c.ctx }
func (c *stdconn) SetContext(ctx interface{}) { c.ctx = ctx }
func (c *stdconn) LocalAddr() net.Addr        { return c.localAddr }
func (c *stdconn) RemoteAddr() net.Addr       { return c.remoteAddr }
func (c *stdconn) Write(data []byte) {
	c.conn.Write(data)
}

type stdin struct {
	c  *stdconn
	in []byte
}

type stderr struct {
	c   *stdconn
	err error
}

// waitForShutdown waits for a signal to shutdown
func (s *stdserver) waitForShutdown() error {
	s.cond.L.Lock()
	s.cond.Wait()
	err := s.serr
	s.cond.L.Unlock()
	return err
}

// signalShutdown signals a shutdown an begins server closing
func (s *stdserver) signalShutdown(err error) {
	s.cond.L.Lock()
	s.serr = err
	s.cond.Signal()
	s.cond.L.Unlock()
}

func stdserve(events Events, listener *listener) error {
	numLoops := events.NumLoops
	if numLoops <= 0 {
		if numLoops == 0 {
			numLoops = 1
		} else {
			numLoops = runtime.NumCPU()
		}
	}

	s := &stdserver{}
	s.events = events
	s.ln = listener
	s.cond = sync.NewCond(&sync.Mutex{})

	if events.Serving != nil {
		var svr Server
		svr.NumLoops = numLoops
		svr.Addr = listener.lnaddr
		action := events.Serving(svr)
		switch action {
		case Shutdown:
			return nil
		}
	}
	for i := 0; i < numLoops; i++ {
		s.loops = append(s.loops, &stdloop{
			idx:   i,
			ch:    make(chan interface{}),
			conns: make(map[*stdconn]bool),
		})
	}
	var ferr error
	defer func() {
		// wait on a signal for shutdown
		ferr = s.waitForShutdown()

		// notify all loops to close by closing all listeners
		for _, l := range s.loops {
			l.ch <- errClosing
		}

		// wait on all loops to main loop channel events
		s.loopwg.Wait()

		// shutdown the listener
		s.ln.close()

		// wait on all listeners to complete
		s.lnwg.Wait()

		// close all connections
		s.loopwg.Add(len(s.loops))
		for _, l := range s.loops {
			l.ch <- errCloseConns
		}
		s.loopwg.Wait()

	}()
	s.loopwg.Add(numLoops)
	for i := 0; i < numLoops; i++ {
		go stdloopRun(s, s.loops[i])
	}
	s.lnwg.Add(1)
	go stdlistenerRun(s, listener)
	return ferr
}

func stdlistenerRun(s *stdserver, ln *listener) {
	var ferr error
	defer func() {
		s.signalShutdown(ferr)
		s.lnwg.Done()
	}()
	for {
		// tcp
		conn, err := ln.ln.Accept()
		if err != nil {
			ferr = err
			return
		}
		l := s.loops[int(atomic.AddUintptr(&s.accepted, 1))%len(s.loops)]
		c := &stdconn{conn: conn, loop: l}
		l.ch <- c
		go func(c *stdconn) {
			var packet [0xFFFF]byte
			for {
				n, err := c.conn.Read(packet[:])
				if err != nil {
					c.conn.SetReadDeadline(time.Time{})
					l.ch <- &stderr{c, err}
					return
				}
				l.ch <- &stdin{c, append([]byte{}, packet[:n]...)}
			}
		}(c)
	}
}

func stdloopRun(s *stdserver, l *stdloop) {
	var err error
	tick := make(chan bool)
	tock := make(chan time.Duration)
	defer func() {
		if l.idx == 0 && s.events.Tick != nil {
			close(tock)
			go func() {
				for range tick {
				}
			}()
		}
		s.signalShutdown(err)
		s.loopwg.Done()
		stdloopEgress(s, l)
		s.loopwg.Done()
	}()
	if l.idx == 0 && s.events.Tick != nil {
		go func() {
			for {
				tick <- true
				delay, ok := <-tock
				if !ok {
					break
				}
				time.Sleep(delay)
			}
		}()
	}
	for {
		select {
		case <-tick:
			delay, action := s.events.Tick()
			switch action {
			case Shutdown:
				err = errClosing
			}
			tock <- delay
		case v := <-l.ch:
			switch v := v.(type) {
			case error:
				err = v
			case *stdconn:
				err = stdloopAccept(s, l, v)
			case *stdin:
				err = stdloopRead(s, l, v.c, v.in)
			case *stderr:
				err = stdloopError(s, l, v.c, v.err)
			}
		}
		if err != nil {
			return
		}
	}
}

func stdloopEgress(s *stdserver, l *stdloop) {
	var closed bool
loop:
	for v := range l.ch {
		switch v := v.(type) {
		case error:
			if v == errCloseConns {
				closed = true
				for c := range l.conns {
					stdloopClose(s, l, c)
				}
			}
		case *stderr:
			stdloopError(s, l, v.c, v.err)
		}
		if len(l.conns) == 0 && closed {
			break loop
		}
	}
}

func stdloopError(s *stdserver, l *stdloop, c *stdconn, err error) error {
	delete(l.conns, c)
	closeEvent := true
	switch atomic.LoadInt32(&c.done) {
	case 0: // read error
		c.conn.Close()
		if err == io.EOF {
			err = nil
		}
	case 1: // closed
		c.conn.Close()
		err = nil
	}
	if closeEvent {
		if s.events.Closed != nil {
			switch s.events.Closed(c, err) {
			case Shutdown:
				return errClosing
			}
		}
	}
	return nil
}

func stdloopRead(s *stdserver, l *stdloop, c *stdconn, in []byte) error {
	if atomic.LoadInt32(&c.done) == 2 {
		// should not ignore reads for detached connections
		c.donein = append(c.donein, in...)
		return nil
	}
	if s.events.Data != nil {
		out, action := s.events.Data(c, in)
		if len(out) > 0 {
			c.conn.Write(out)
		}
		switch action {
		case Shutdown:
			return errClosing
		case Close:
			return stdloopClose(s, l, c)
		}
	}
	return nil
}

func stdloopClose(s *stdserver, l *stdloop, c *stdconn) error {
	atomic.StoreInt32(&c.done, 1)
	c.conn.SetReadDeadline(time.Now())
	return nil
}

func stdloopAccept(s *stdserver, l *stdloop, c *stdconn) error {
	l.conns[c] = true
	c.localAddr = s.ln.lnaddr
	c.remoteAddr = c.conn.RemoteAddr()

	if s.events.Opened != nil {
		out, opts, action := s.events.Opened(c)
		if len(out) > 0 {
			c.conn.Write(out)
		}
		if opts.TCPKeepAlive > 0 {
			if c, ok := c.conn.(*net.TCPConn); ok {
				c.SetKeepAlive(true)
				c.SetKeepAlivePeriod(opts.TCPKeepAlive)
			}
		}
		switch action {
		case Shutdown:
			return errClosing
		case Close:
			return stdloopClose(s, l, c)
		}
	}

	return nil
}
