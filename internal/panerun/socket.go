package panerun

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// maxSocketPath is the longest unix socket path the platform binds:
// sockaddr_un.sun_path holds it and a trailing NUL (104 bytes on macOS, 108
// on Linux). A longer path fails bind and connect with a bare EINVAL.
var maxSocketPath = len(syscall.RawSockaddrUnix{}.Path) - 1

// checkSocketPath refuses a path the kernel would refuse, saying why, where
// the kernel would only say "invalid argument".
func checkSocketPath(socket string) error {
	if len(socket) > maxSocketPath {
		return fmt.Errorf("panerun: socket path %s is %d bytes; a unix socket path holds at most %d", socket, len(socket), maxSocketPath)
	}
	return nil
}

// SocketDir makes a new private directory, named from pattern as
// os.MkdirTemp does, for runner sockets with the given base names. It is
// made under the temp dir when every socket fits there, else under /tmp,
// so a long TMPDIR (a deep sandbox or worktree path) never leaves the
// Console with sockets it cannot bind. The caller removes the directory.
func SocketDir(pattern string, names ...string) (string, error) {
	longest := 0
	for _, n := range names {
		longest = max(longest, len(n))
	}
	bases := []string{os.TempDir()}
	if filepath.Clean(bases[0]) != "/tmp" {
		bases = append(bases, "/tmp")
	}
	var tried []string
	for _, base := range bases {
		dir, err := os.MkdirTemp(base, pattern)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s: %v", base, err))
			continue
		}
		if len(filepath.Join(dir, "x"))-1+longest <= maxSocketPath {
			return dir, nil
		}
		_ = os.Remove(dir)
		tried = append(tried, fmt.Sprintf("%s: a socket in it would pass %d bytes", base, maxSocketPath))
	}
	return "", fmt.Errorf("panerun: no directory short enough for a unix socket (%v)", tried)
}
