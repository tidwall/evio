package internal

// SetKeepAlive sets the keepalive for the connection
func SetKeepAlive(fd, secs int) error {
	// OpenBSD has no user-settable per-socket TCP keepalive options.
	return nil
}
