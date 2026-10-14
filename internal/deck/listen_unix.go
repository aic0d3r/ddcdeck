//go:build unix

package deck

import (
	"net"
	"syscall"
)

// listenPrivate creates the socket mode 0600 from the start (no chmod race).
func listenPrivate(path string) (net.Listener, error) {
	old := syscall.Umask(0o177)
	defer syscall.Umask(old)
	return net.Listen("unix", path)
}
