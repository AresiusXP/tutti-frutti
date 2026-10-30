//go:build unix

package moderation

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// maxSocketPath is the most a Unix socket path may hold on the supported
// systems (sun_path is 104 bytes on macOS and 108 on Linux, with the NUL).
const maxSocketPath = 100

var umaskMu sync.Mutex

// Listen creates the control socket at path: an absolute path under a
// directory the server can write to (the chart mounts an emptyDir for it, which
// works with a read-only root filesystem), mode 0600, owned by the server's own
// user, and removed when the listener is closed. A stale socket left by a
// crashed process is replaced; anything else at that path is refused, never
// deleted.
//
// The directory is only created when it is missing (mode 0700, by us). An
// existing directory is never chmod'ed or chown'ed: the socket's own 0600 is
// the access control, and the process has no business changing a directory it
// may not own (a mounted volume). Whether it is usable is decided by creating
// the socket; when that is refused the error names the path, the uid and the
// directory's owner and mode, which is what an operator needs to fix the mount.
func Listen(path string) (net.Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("moderation socket %q: the path must be absolute", path)
	}
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("moderation socket %q: the path is longer than %d bytes", path, maxSocketPath)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, socketErr(path, dir, "creating the directory", err)
	}
	switch fi, err := os.Lstat(path); {
	case err == nil && fi.Mode()&os.ModeSocket != 0:
		if err := os.Remove(path); err != nil {
			return nil, socketErr(path, dir, "removing the stale socket", err)
		}
	case err == nil:
		return nil, fmt.Errorf("moderation socket %q exists and is not a socket: refusing to remove it", path)
	case !errors.Is(err, os.ErrNotExist):
		return nil, socketErr(path, dir, "inspecting the path", err)
	}

	// The socket is created with the process umask: make it 0600 from the first
	// instant, then check.
	umaskMu.Lock()
	old := syscall.Umask(0o177)
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	umaskMu.Unlock()
	if err != nil {
		return nil, socketErr(path, dir, "creating the socket", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("moderation socket: %w", err)
	}
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(true)
	}
	return l, nil
}

// socketErr wraps a failure of Listen. A permission or read-only error says who
// the server runs as and what the directory looks like (its closest existing
// ancestor when it is missing), so the log line explains the fix by itself.
func socketErr(path, dir, doing string, err error) error {
	if !errors.Is(err, fs.ErrPermission) && !errors.Is(err, syscall.EROFS) {
		return fmt.Errorf("moderation socket %q: %s: %w", path, doing, err)
	}
	probe := dir
	for {
		fi, statErr := os.Stat(probe)
		if statErr == nil {
			owner := "unknown"
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				owner = fmt.Sprintf("uid %d gid %d", st.Uid, st.Gid)
			}
			return fmt.Errorf("moderation socket %q: %s: %w: the server runs as uid %d gid %d and %q (owner %s, mode %04o) does not let it create files: "+
				"mount a writable directory there (the chart's emptyDir with the pod's fsGroup) or point MODERATION_SOCKET at one",
				path, doing, err, os.Geteuid(), os.Getegid(), probe, owner, uint32(fi.Mode().Perm()))
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return fmt.Errorf("moderation socket %q: %s (the server runs as uid %d): %w", path, doing, os.Geteuid(), err)
		}
		probe = parent
	}
}
