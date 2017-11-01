package evio

import (
	"crypto/tls"
	"io"
)

func TLS(events Events, config *tls.Config) Events {
	return Translate(events,
		func(rw io.ReadWriter) io.ReadWriter {
			return tls.Server(&nopConn{rw}, config)
		},
	)
}
