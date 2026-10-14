//go:build !unix

package deck

import "net"

// shortcut: plain unix socket, no private mode; replace with a named pipe when the Windows port starts.
func listenPrivate(path string) (net.Listener, error) { return net.Listen("unix", path) }
