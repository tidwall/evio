//+build !darwin,!freebsd,!linux

package shiny

func eventServe(net, addr string,
	handle func(id int, data []byte, ctx interface{}) (send []byte, keepopen bool),
	accept func(id int, addr string, wake func(), ctx interface{}) (send []byte, keepopen bool),
	closed func(id int, err error, ctx interface{}),
	ticker func(ctx interface{}) (keepopen bool),
	context interface{}) error {
	return compatServe(net, addr, handle, accept, closed, ticker, context)
}
