// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package procname // import "go.opentelemetry.io/obi/pkg/internal/procname"

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// readInRoot reads the absolute path p inside root (/proc/<pid>/root). openat2
// with RESOLVE_IN_ROOT resolves every symlink relative to root, so a file in a
// container can never point OBI at a host file. Kernels without openat2 (<5.6)
// fall back to rejecting any symlink on the path.
func readInRoot(root, p string) ([]byte, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("not an absolute path: %q", p)
	}
	dirfd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirfd)

	fd, err := unix.Openat2(dirfd, strings.TrimPrefix(p, "/"), &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		Resolve: unix.RESOLVE_IN_ROOT | unix.RESOLVE_NO_MAGICLINKS,
	})
	if errors.Is(err, unix.ENOSYS) {
		fd, err = openNoSymlinks(dirfd, p)
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), p)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxFileBytes {
		return nil, fmt.Errorf("not a regular file or too large: %q", p)
	}
	return io.ReadAll(io.LimitReader(f, maxFileBytes+1))
}

// openNoSymlinks opens p below dirfd one component at a time with O_NOFOLLOW,
// so any symlink (or "..") on the path makes it fail.
func openNoSymlinks(dirfd int, p string) (int, error) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	cur := dirfd
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			if cur != dirfd {
				unix.Close(cur)
			}
			return -1, fmt.Errorf("invalid path %q", p)
		}
		flags := unix.O_NOFOLLOW | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_PATH | unix.O_DIRECTORY
		} else {
			flags |= unix.O_RDONLY
		}
		next, err := unix.Openat(cur, part, flags, 0)
		if cur != dirfd {
			unix.Close(cur)
		}
		if err != nil {
			return -1, err
		}
		cur = next
	}
	return cur, nil
}
