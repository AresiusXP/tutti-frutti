//go:build !unix

package moderation

import (
	"errors"
	"net"
)

// Listen is not supported off Unix: the server only runs on Linux.
func Listen(string) (net.Listener, error) {
	return nil, errors.New("moderation socket: unix domain sockets are not supported on this system")
}
