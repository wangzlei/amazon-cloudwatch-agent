// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package procname

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadInRootStaysInRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "package.json")
	require.NoError(t, os.WriteFile(outside, []byte(`{"name":"host-file"}`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "srv"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "srv", "package.json"), []byte(`{"name":"in-root"}`), 0o644))
	// an absolute symlink inside the root must not reach the host file
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape.json")))

	data, err := readInRoot(root, "/srv/package.json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"in-root"}`, string(data))

	data, err = readInRoot(root, "/escape.json")
	if err == nil {
		assert.NotContains(t, string(data), "host-file")
	}
	_, err = readInRoot(root, "/srv/../../"+filepath.Base(filepath.Dir(outside))+"/package.json")
	assert.Error(t, err)
}
