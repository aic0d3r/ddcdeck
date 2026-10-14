// Package hotplug reports display connect/disconnect via kernel DRM uevents.
package hotplug

import (
	"bytes"
	"context"
	"syscall"
	"time"
)

// Watch calls onChange after DRM uevents (debounced; docks fire bursts) until ctx is done.
func Watch(ctx context.Context, onChange func()) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: 1}); err != nil {
		syscall.Close(fd)
		return err
	}
	go func() { <-ctx.Done(); syscall.Close(fd) }()
	kick := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-kick:
			}
			time.Sleep(1500 * time.Millisecond) // let MST settle and coalesce the burst
			for len(kick) > 0 {
				<-kick
			}
			onChange()
		}
	}()
	buf := make([]byte, 16<<10)
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if bytes.Contains(buf[:n], []byte("\x00SUBSYSTEM=drm\x00")) {
			select {
			case kick <- struct{}{}:
			default:
			}
		}
	}
}
