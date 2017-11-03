// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/kataras/iris"
)

var res string

func main() {
	var port int
	flag.IntVar(&port, "port", 8080, "server port")
	flag.Parse()
	go log.Printf("http server started on port %d", port)
	app := iris.New()
	app.Get("/", func(ctx iris.Context) {
		ctx.WriteString("Hello World!\r\n")
	})
	err := app.Run(iris.Addr(fmt.Sprintf(":%d", port)))
	if err != nil {
		log.Fatal(err)
	}

}
