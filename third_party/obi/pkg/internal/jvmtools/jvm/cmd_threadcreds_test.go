// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package jvm

import (
	"os"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// The credential switch for a JVM attach must affect only the attaching thread,
// never the rest of the process (needs root / CAP_SETUID+CAP_SETGID).
func TestSetThreadCredentialsDoNotLeakToOtherThreads(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (CAP_SETUID/CAP_SETGID)")
	}
	const nobody = 65534
	type ids struct{ euid, egid int }
	switched := make(chan ids)
	release := make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // never unlocked: the thread dies with the goroutine
		if err := setThreadEGID(nobody); err != nil {
			errc <- err
			return
		}
		if err := setThreadEUID(nobody); err != nil {
			errc <- err
			return
		}
		switched <- ids{syscall.Geteuid(), syscall.Getegid()}
		<-release
		errc <- nil
	}()

	var got ids
	select {
	case got = <-switched:
	case err := <-errc:
		t.Fatal(err)
	}
	require.Equal(t, ids{nobody, nobody}, got, "attaching thread switched")

	// meanwhile, on another thread, the process is still root
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	require.Equal(t, 0, syscall.Geteuid())
	require.Equal(t, 0, syscall.Getegid())
	close(release)
	require.NoError(t, <-errc)
	require.Equal(t, 0, syscall.Geteuid())
}
