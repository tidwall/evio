package main

import (
	"crypto/tls"
	"log"

	"github.com/tidwall/evio"
)

// type tlsconn struct {
// 	inrd, outrd *io.PipeReader
// 	inwr, outwr *io.PipeWriter
// }
// func (c *tlsconn) Read(p []byte) (int, error) {
// 	n, err := c.inrd.Read(p)
// 	fmt.Printf("%v %v %v\n", len(p), n, err)
// 	return n, err
// }
// func (c *tlsconn) Write(p []byte) (int, error)        { panic("unsupported") }

// func (c *tlsconn) LocalAddr() net.Addr  { panic("unsupported") }
// func (c *tlsconn) RemoteAddr() net.Addr { panic("unsupported") }
// func (c *tlsconn) Close() error         { panic("unsupported") }
// func (c *tlsconn) SetDeadline(t time.Time) error      { panic("unsupported") }
// func (c *tlsconn) SetReadDeadline(t time.Time) error  { panic("unsupported") }
// func (c *tlsconn) SetWriteDeadline(t time.Time) error { panic("unsupported") }
const (
	kindin  = 1
	kindout = 2
)

type tconn struct {
	instatus  int // 0: not, 1: active, 2: torn down
	outstatus int // 0: not, 1: active, 2: torn down
}

func tconnget(idconn map[int]*tconn, id int, b []byte, kind int) *tconn {
	c := idconn[id]
	if c == nil {
		if b == nil {
			return nil
		}
		c = &tconn{}
		idconn[id] = c
	}
	switch kind {
	case kindin:
		if b == nil {
			if c.instatus == 1 {
				c.instatus = 2
				println("TODO: close input stream")
			}
			if c.outstatus != 1 {
				delete(idconn, id)
			}
			return nil
		}
		switch c.instatus {
		case 0:
			println("TODO: init input stream")
		case 1:
			// keep operating
		case 2:
			panic("input already torn down")
		}
	case kindout:
		if b == nil {
			if c.outstatus == 1 {
				c.outstatus = 2
				println("TODO: close output stream")
			}
			if c.instatus != 1 {
				delete(idconn, id)
			}
			return nil
		}
		switch c.outstatus {
		case 0:
			println("TODO: init output stream")
		case 1:
			// keep operating
		case 2:
			panic("output already torn down")
		}
	}
	return c
}

func tlsTranslator(config *tls.Config) (translateIn, translateOut func(id int, b []byte) []byte) {
	idconn := make(map[int]*tconn)
	translateIn = func(id int, b []byte) []byte {
		c := tconnget(idconn, id, b, kindin)
		if c == nil {
			println("input done")
			return nil
		}
		return b
	}
	translateOut = func(id int, b []byte) []byte {
		c := tconnget(idconn, id, b, kindout)
		if c == nil {
			println("output done")
			return nil
		}
		return b
	}

	// // const (
	// // 	kin  = 1
	// // 	kout = 2
	// // )
	// // idconn := make(map[int]*tlsconn)
	// // get := func(id int, kind int) *tlsconn {
	// // 	c, ok := idconn[id]
	// // 	// 	if !ok {
	// // 	// 		c = &tlsconn{}
	// // 	// 		c.inrd, c.inwr = io.Pipe()
	// // 	// 		c.outrd, c.outwr = io.Pipe()
	// // 	// 		idconn[id] = c
	// // 	// 		tlssc := tls.Server(c, config)
	// // 	// 		go func(tlssc *tls.Conn) {
	// // 	// 			var packet [2048]byte
	// // 	// 			for {
	// // 	// 				n, err := tlssc.Read(packet[:])
	// // 	// 				if err != nil {
	// // 	// 					panic(err)
	// // 	// 				}
	// // 	// 				println("IN:", n)
	// // 	// 			}
	// // 	// 		}(tlssc)
	// // 	// 	}
	// // 	// 	return c
	// // }

	// translateIn = func(id int, b []byte) []byte {
	// 	// c := get(id, kin)
	// 	// _ = c
	// 	// if b == nil {
	// 	// 	c.inwr.Close()
	// 	// 	delete(idconn, id)
	// 	// 	return nil
	// 	// }
	// 	// for len(b) > 0 {
	// 	// 	n, err := c.inwr.Write(b)
	// 	// 	if err != nil {
	// 	// 		panic(err)
	// 	// 	}
	// 	// 	b = b[n:]
	// 	// }
	// 	return b
	// }
	// translateOut = func(id int, b []byte) []byte {
	// 	c := get(id, kout)
	// 	_ = c
	// 	// if b == nil {

	// 	// 	c.inwr.Close()

	// 	// 	return nil
	// 	// }
	// 	// _ = c
	// 	return b
	// }
	return
}

func main() {
	var events evio.Events

	events.Serving = func(wake func(id int) bool) (action evio.Action) {
		log.Print("echo server started on port 5000")
		return
	}
	events.Opened = func(id int, addr evio.Addr) (out []byte, opts evio.Options, action evio.Action) {
		println("opened")
		//		opts.OutRd, opts.OutWr = io.Pipe()

		return
	}

	// cer, err := tls.LoadX509KeyPair("server.crt", "server.key")
	// if err != nil {
	// 	panic(err)
	// }
	// config := &tls.Config{Certificates: []tls.Certificate{cer}}
	// events.TranslateIn, events.TranslateOut = tlsTranslator(config)

	events.Data = func(id int, in []byte) (out []byte, action evio.Action) {
		out = in
		return
	}

	log.Fatal(evio.Serve(events, "tcp://0.0.0.0:5000"))
}
