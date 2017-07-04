package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/tidwall/redcon"
	"github.com/tidwall/shiny"
)

func main() {
	var port int
	var appendonly string
	flag.IntVar(&port, "port", 6380, "server port")
	flag.StringVar(&appendonly, "appendonly", "no", "use appendonly file (yes or no)")
	flag.Parse()

	var resp []byte
	var aofw []byte
	var args [][]byte
	var shutdown bool
	var started bool
	var bufs = make(map[int][]byte)
	var keys = make(map[string]string)

	var f *os.File
	if appendonly == "yes" {
		var err error
		f, err = os.OpenFile("appendonly.aof", os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()

		rd := redcon.NewReader(f)
		for {
			cmd, err := rd.ReadCommand()
			if err == io.EOF {
				break
			} else if err != nil {
				log.Fatal(err)
			}
			switch strings.ToUpper(string(cmd.Args[0])) {
			default:
				log.Fatal("bad aof")
			case "SET":
				keys[string(cmd.Args[1])] = string(cmd.Args[2])
			}
		}
	}
	log.Fatal(shiny.Serve("tcp", fmt.Sprintf(":%d", port),
		func(id int, data []byte, ctx interface{}) (send []byte, keepopen bool) {
			if shutdown {
				return nil, false
			}
			buf := bufs[id]
			if len(buf) > 0 {
				data = append(buf, data...)
			}
			keepopen = true
			resp = resp[:0]
			aofw = aofw[:0]
			var complete bool
			var err error
			for {
				complete, args, _, data, err = redcon.ReadNextCommand(data, args[:0])
				if err != nil {
					keepopen = false
					resp = redcon.AppendError(resp, err.Error())
					break
				}
				if !complete {
					break
				}
				switch strings.ToUpper(string(args[0])) {
				default:
					resp = redcon.AppendError(resp, fmt.Sprintf("ERR unknown command '%s'", args[0]))
				case "PING":
					if len(args) > 2 {
						resp = redcon.AppendError(resp, fmt.Sprintf("ERR wrong number of arguments for '%s' command", args[0]))
						continue
					} else if len(args) == 1 {
						resp = redcon.AppendString(resp, "PONG")
					} else {
						resp = redcon.AppendBulk(resp, args[1])
					}
				case "QUIT":
					keepopen = false
					resp = redcon.AppendOK(resp)
				case "SHUTDOWN":
					shutdown = true
					keepopen = false
					resp = redcon.AppendOK(resp)
				case "SET":
					if len(args) != 3 {
						resp = redcon.AppendError(resp, fmt.Sprintf("ERR wrong number of arguments for '%s' command", args[0]))
						continue
					}
					keys[string(args[1])] = string(args[2])
					resp = redcon.AppendOK(resp)
					if appendonly == "yes" {
						// create the append only entry
						aofw = redcon.AppendArray(aofw, 3)
						aofw = redcon.AppendBulkString(aofw, "SET")
						aofw = redcon.AppendBulk(aofw, args[1])
						aofw = redcon.AppendBulk(aofw, args[2])
					}
				case "GET":
					if len(args) != 2 {
						resp = redcon.AppendError(resp, fmt.Sprintf("ERR wrong number of arguments for '%s' command", args[0]))
						continue
					}
					val, ok := keys[string(args[1])]
					if !ok {
						resp = redcon.AppendNull(resp)
					} else {
						resp = redcon.AppendBulkString(resp, val)
					}
				case "DEL":
					if len(args) < 2 {
						resp = redcon.AppendError(resp, fmt.Sprintf("ERR wrong number of arguments for '%s' command", args[0]))
						continue
					}
					var n int64
					for i := 1; i < len(args); i++ {
						if _, ok := keys[string(args[i])]; ok {
							delete(keys, string(args[i]))
							n++
						}
					}
					resp = redcon.AppendInt(resp, n)
				}
			}
			if len(data) > 0 {
				bufs[id] = append(buf[:0], data...)
			} else if len(buf) > 0 {
				bufs[id] = buf[:0]
			}
			if len(aofw) > 0 {
				if _, err := f.Write(aofw); err != nil {
					log.Fatal(err)
				}
			}
			return resp, keepopen
		},
		// accept - a new client socket has opened
		func(id int, addr string, wake func(), ctx interface{}) (send []byte, keepopen bool) {
			if shutdown {
				return nil, false
			}
			// create a new socket context here
			return nil, true
		},
		// closed - a client socket has closed
		func(id int, err error, ctx interface{}) {
			// teardown the socket context here
			delete(bufs, id)
		},
		// ticker - a ticker that fires between 1 and 1/20 of a second
		// depending on the traffic.
		func(ctx interface{}) (keepserveropen bool) {
			if shutdown {
				// do server teardown here
				return false
			}
			if !started {
				fmt.Printf("redis(ish) server started on port %d\n", port)
				started = true
			}
			// perform various non-socket-io related operation here
			return true
		},
		// an optional user-defined context
		nil))
}
