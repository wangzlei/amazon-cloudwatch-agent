// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootOwnedNotWritable(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "obi.yaml")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(f, link))

	if os.Geteuid() != 0 {
		// files of the test user are refused: only root may own the eBPF config
		assert.ErrorContains(t, rootOwnedNotWritable(f), "not root")
		assert.ErrorContains(t, rootOwnedNotWritable(link), "not root")
		assert.Error(t, rootOwnedNotWritable(filepath.Join(dir, "missing")))
		return
	}
	assert.NoError(t, rootOwnedNotWritable(f))
	assert.ErrorContains(t, rootOwnedNotWritable(link), "symlink")
	require.NoError(t, os.Chmod(f, 0o664))
	assert.ErrorContains(t, rootOwnedNotWritable(f), "writable by group or others")
	require.NoError(t, os.Chmod(f, 0o646))
	assert.ErrorContains(t, rootOwnedNotWritable(f), "writable by group or others")
	require.NoError(t, os.Chown(f, 65534, 0))
	require.NoError(t, os.Chmod(f, 0o644))
	assert.ErrorContains(t, rootOwnedNotWritable(f), "not root")
}
