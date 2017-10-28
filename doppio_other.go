// +build !darwin,!netbsd,!freebsd,!openbsd,!dragonfly,!linux

package doppio

import "os"

func (ln *listener) close() {
	if ln.ln != nil {
		ln.ln.Close()
	}
	if ln.network == "unix" {
		os.RemoveAll(ln.addr)
	}
}

func (ln *listener) system() error {
	return nil
}

func serve(events Events, lns []*listener) error {
	return servestdlib(events, lns)
}
