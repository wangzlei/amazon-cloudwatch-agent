// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package javaagent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"go.opentelemetry.io/obi/pkg/appolly/app"
	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
	"go.opentelemetry.io/obi/pkg/appolly/discover/exec"
	"go.opentelemetry.io/obi/pkg/ebpf"
	"go.opentelemetry.io/obi/pkg/obi"
)

func TestRunIfCurrentAttachHoldsLockWhileRunningAction(t *testing.T) {
	injector := &JavaInjector{}
	attachID := injector.nextAttachID()

	actionStarted := make(chan struct{})
	finishAction := make(chan struct{})
	actionDone := make(chan error, 1)
	go func() {
		actionDone <- injector.runIfCurrentAttach(attachID, func() error {
			close(actionStarted)
			<-finishAction
			return nil
		})
	}()

	<-actionStarted
	lockWasAvailable := injector.mu.TryLock()
	if lockWasAvailable {
		injector.mu.Unlock()
	}

	nextAttachID := make(chan int64, 1)
	go func() {
		nextAttachID <- injector.nextAttachID()
	}()

	close(finishAction)
	require.NoError(t, <-actionDone)
	require.Equal(t, int64(2), <-nextAttachID)
	require.False(t, lockWasAvailable)

	actionRan := false
	require.NoError(t, injector.runIfCurrentAttach(attachID, func() error {
		actionRan = true
		return nil
	}))
	require.False(t, actionRan)
}

func TestJavaInjector_CopyAgent(t *testing.T) {
	oldJavaAgentBytes := embeddedJavaAgentBytes
	embeddedJavaAgentBytes = []byte("test agent content")
	t.Cleanup(func() {
		embeddedJavaAgentBytes = oldJavaAgentBytes
	})

	tests := []struct {
		name          string
		setupTempDir  func(t *testing.T, pid app.PID) string
		envVars       map[string]string
		pid           app.PID
		expectError   bool
		errorContains string
		verifyFile    bool
	}{
		{
			name: "successful copy to /tmp",
			setupTempDir: func(t *testing.T, _ app.PID) string {
				tmpDir := t.TempDir()
				procRoot := filepath.Join(tmpDir, "proc", "root")
				require.NoError(t, os.MkdirAll(filepath.Join(procRoot, "tmp"), 0o755))
				return tmpDir
			},
			envVars:     map[string]string{},
			pid:         1000,
			expectError: false,
			verifyFile:  true,
		},
		{
			name: "TMPDIR absolute path outside process root is ignored",
			setupTempDir: func(t *testing.T, _ app.PID) string {
				tmpDir := t.TempDir()
				procRoot := filepath.Join(tmpDir, "proc", "root")
				require.NoError(t, os.MkdirAll(filepath.Join(procRoot, "tmp"), 0o755))
				return tmpDir
			},
			envVars: map[string]string{
				"TMPDIR": "/proc/1/root/etc",
			},
			pid:         1000,
			expectError: false,
			verifyFile:  true,
		},
		{
			name: "TMPDIR relative path escape is ignored",
			setupTempDir: func(t *testing.T, _ app.PID) string {
				tmpDir := t.TempDir()
				procRoot := filepath.Join(tmpDir, "proc", "root")
				require.NoError(t, os.MkdirAll(filepath.Join(procRoot, "tmp"), 0o755))
				return tmpDir
			},
			envVars: map[string]string{
				"TMPDIR": "../../../etc",
			},
			pid:         1000,
			expectError: false,
			verifyFile:  true,
		},
		{
			name: "fallback to /var/tmp when /tmp not available",
			setupTempDir: func(t *testing.T, _ app.PID) string {
				tmpDir := t.TempDir()
				procRoot := filepath.Join(tmpDir, "proc", "root")
				require.NoError(t, os.MkdirAll(filepath.Join(procRoot, "var", "tmp"), 0o755))
				return tmpDir
			},
			envVars:     map[string]string{},
			pid:         1000,
			expectError: false,
			verifyFile:  true,
		},
		{
			name: "error when no temp directory available",
			setupTempDir: func(t *testing.T, _ app.PID) string {
				tmpDir := t.TempDir()
				procRoot := filepath.Join(tmpDir, "proc", "root")
				require.NoError(t, os.MkdirAll(procRoot, 0o755))
				return tmpDir
			},
			envVars:       map[string]string{},
			pid:           1000,
			expectError:   true,
			errorContains: "error accessing temp directory",
			verifyFile:    false,
		},
		{
			name: "error when target directory not writable",
			setupTempDir: func(t *testing.T, _ app.PID) string {
				tmpDir := t.TempDir()
				procRoot := filepath.Join(tmpDir, "proc", "root")
				tmpPath := filepath.Join(procRoot, "tmp")
				require.NoError(t, os.MkdirAll(tmpPath, 0o755))
				require.NoError(t, os.Chmod(tmpPath, 0o555))
				return tmpDir
			},
			envVars:       map[string]string{},
			pid:           1000,
			expectError:   true,
			errorContains: "unable to create target OBI java agent",
			verifyFile:    false,
		},
		{
			name: "agent content correctly copied",
			setupTempDir: func(t *testing.T, _ app.PID) string {
				tmpDir := t.TempDir()
				procRoot := filepath.Join(tmpDir, "proc", "root")
				require.NoError(t, os.MkdirAll(filepath.Join(procRoot, "tmp"), 0o755))
				return tmpDir
			},
			envVars:     map[string]string{},
			pid:         1000,
			expectError: false,
			verifyFile:  true,
		},
		{
			name: "copy does not follow existing symlink target",
			setupTempDir: func(t *testing.T, _ app.PID) string {
				tmpDir := t.TempDir()
				procRoot := filepath.Join(tmpDir, "proc", "root")
				targetDir := filepath.Join(procRoot, "tmp")
				require.NoError(t, os.MkdirAll(targetDir, 0o755))

				victim := filepath.Join(tmpDir, "victim")
				require.NoError(t, os.WriteFile(victim, []byte("do not overwrite"), 0o644))
				require.NoError(t, os.Symlink(victim, filepath.Join(targetDir, ObiJavaAgentFileName)))
				return tmpDir
			},
			envVars:     map[string]string{},
			pid:         1000,
			expectError: false,
			verifyFile:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := tt.setupTempDir(t, tt.pid)

			// Override the root directory function
			originalRootFunc := rootDirForPID
			defer func() { rootDirForPID = originalRootFunc }()
			rootDirForPID = func(_ app.PID) string {
				return filepath.Join(tmpDir, "proc", "root")
			}

			injector := &JavaInjector{
				cfg: &obi.DefaultConfig,
				log: slog.With("component", "javaagent.Injector"),
			}

			ie := &ebpf.Instrumentable{
				FileInfo: exec.New(exec.Init{
					Pid: tt.pid,
					Service: svc.Attrs{
						EnvVars: tt.envVars,
					},
				}),
				Type: svc.InstrumentableJava,
			}

			resultPath, err := injector.copyAgent(ie)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, resultPath)

				if tt.verifyFile {
					// Verify the file was created in the host filesystem
					procRoot := filepath.Join(tmpDir, "proc", "root")
					expectedHostPath := filepath.Join(procRoot, strings.TrimPrefix(resultPath, "/"))

					info, err := os.Stat(expectedHostPath)
					require.NoError(t, err)
					assert.False(t, info.IsDir())
					assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())

					// Verify content matches
					originalContent := embeddedJavaAgentBytes
					copiedContent, err := os.ReadFile(expectedHostPath)
					require.NoError(t, err)
					assert.Equal(t, originalContent, copiedContent)

					victimPath := filepath.Join(tmpDir, "victim")
					if _, err := os.Stat(victimPath); err == nil {
						victimContent, readErr := os.ReadFile(victimPath)
						require.NoError(t, readErr)
						assert.Equal(t, []byte("do not overwrite"), victimContent)
					}
				}
			}
		})
	}
}

func TestFindTempDir(t *testing.T) {
	tests := []struct {
		name        string
		setupDirs   func(t *testing.T, root string)
		expectError bool
		expectedDir string
	}{
		{
			name: "/tmp",
			setupDirs: func(t *testing.T, root string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "custom", "tmp"), 0o755))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "tmp"), 0o755))
			},
			expectedDir: "/tmp",
		},
		{
			name: "fallback to /var/tmp when /tmp missing",
			setupDirs: func(t *testing.T, root string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "var", "tmp"), 0o755))
			},
			expectedDir: "/var/tmp",
		},
		{
			name: "symlinked /tmp is not used",
			setupDirs: func(t *testing.T, root string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "real"), 0o755))
				require.NoError(t, os.Symlink("/real", filepath.Join(root, "tmp")))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "var", "tmp"), 0o755))
			},
			expectedDir: "/var/tmp",
		},
		{
			name: "symlink on the /var/tmp path is not used",
			setupDirs: func(t *testing.T, root string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "real", "tmp"), 0o755))
				require.NoError(t, os.Symlink("real", filepath.Join(root, "var")))
			},
			expectError: true,
		},
		{
			name: "error when no temp dir available",
			setupDirs: func(t *testing.T, root string) {
				require.NoError(t, os.MkdirAll(root, 0o755))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.setupDirs(t, root)

			tmpDir, fd, err := findTempDir(root)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "couldn't find suitable temp directory")
			} else {
				require.NoError(t, err)
				unix.Close(fd)
				assert.Equal(t, tt.expectedDir, tmpDir)
			}
		})
	}
}

// The target's file system must not be able to redirect the jar write outside
// its root (to the host), and the target's TMPDIR must not be used.
func TestJavaInjector_CopyAgentStaysInRoot(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, root, host string)
		envVars map[string]string
		wantErr bool
		wantDir string // directory, inside root, that must hold the jar
	}{
		{
			name: "absolute /tmp symlink to a host directory",
			setup: func(t *testing.T, root, host string) {
				require.NoError(t, os.Symlink(host, filepath.Join(root, "tmp")))
			},
			wantErr: true,
		},
		{
			name: "relative /tmp symlink escaping the root",
			setup: func(t *testing.T, root, host string) {
				rel, err := filepath.Rel(root, host)
				require.NoError(t, err)
				require.NoError(t, os.Symlink(rel, filepath.Join(root, "tmp")))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "var", "tmp"), 0o755))
			},
			wantDir: "var/tmp",
		},
		{
			name: "TMPDIR of the target is ignored",
			setup: func(t *testing.T, root, _ string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "custom"), 0o755))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "tmp"), 0o755))
			},
			envVars: map[string]string{"TMPDIR": "/custom"},
			wantDir: "tmp",
		},
		{
			name: "existing jar symlink to a host file is replaced, not followed",
			setup: func(t *testing.T, root, host string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "tmp"), 0o755))
				require.NoError(t, os.Symlink(filepath.Join(host, ObiJavaAgentFileName), filepath.Join(root, "tmp", ObiJavaAgentFileName)))
			},
			wantDir: "tmp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "root")
			host := filepath.Join(base, "host")
			require.NoError(t, os.MkdirAll(root, 0o755))
			require.NoError(t, os.MkdirAll(host, 0o755))
			victim := filepath.Join(host, ObiJavaAgentFileName)
			require.NoError(t, os.WriteFile(victim, []byte("do not overwrite"), 0o644))
			tt.setup(t, root, host)

			originalRootFunc := rootDirForPID
			defer func() { rootDirForPID = originalRootFunc }()
			rootDirForPID = func(_ app.PID) string { return root }

			injector := &JavaInjector{cfg: &obi.DefaultConfig, log: slog.With("component", "javaagent.Injector")}
			ie := &ebpf.Instrumentable{
				FileInfo: exec.New(exec.Init{Pid: 1000, Service: svc.Attrs{EnvVars: tt.envVars}}),
				Type:     svc.InstrumentableJava,
			}

			path, err := injector.copyAgent(ie)

			// the host directory is never written
			entries, rerr := os.ReadDir(host)
			require.NoError(t, rerr)
			assert.Len(t, entries, 1)
			content, rerr := os.ReadFile(victim)
			require.NoError(t, rerr)
			assert.Equal(t, []byte("do not overwrite"), content)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "/"+tt.wantDir+"/"+ObiJavaAgentFileName, path)
			jar := filepath.Join(root, tt.wantDir, ObiJavaAgentFileName)
			info, lerr := os.Lstat(jar)
			require.NoError(t, lerr)
			assert.True(t, info.Mode().IsRegular())
			assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
			copied, rerr := os.ReadFile(jar)
			require.NoError(t, rerr)
			assert.Equal(t, embeddedJavaAgentBytes, copied)
			// no temp file is left behind
			left, rerr := os.ReadDir(filepath.Dir(jar))
			require.NoError(t, rerr)
			assert.Len(t, left, 1)
		})
	}
}

func TestDirOK(t *testing.T) {
	tests := []struct {
		name      string
		setupDirs func(t *testing.T) (root string, dir string)
		expected  bool
	}{
		{
			name: "valid directory exists",
			setupDirs: func(t *testing.T) (string, string) {
				root := t.TempDir()
				dir := "/testdir"
				require.NoError(t, os.MkdirAll(filepath.Join(root, strings.TrimPrefix(dir, "/")), 0o755))
				return root, dir
			},
			expected: true,
		},
		{
			name: "directory does not exist",
			setupDirs: func(t *testing.T) (string, string) {
				root := t.TempDir()
				return root, "/nonexistent"
			},
			expected: false,
		},
		{
			name: "path is a file not a directory",
			setupDirs: func(t *testing.T) (string, string) {
				root := t.TempDir()
				file := "/testfile"
				require.NoError(t, os.WriteFile(filepath.Join(root, strings.TrimPrefix(file, "/")), []byte("content"), 0o644))
				return root, file
			},
			expected: false,
		},
		{
			name: "nested directory exists",
			setupDirs: func(t *testing.T) (string, string) {
				root := t.TempDir()
				dir := "/nested/path/dir"
				require.NoError(t, os.MkdirAll(filepath.Join(root, strings.TrimPrefix(dir, "/")), 0o755))
				return root, dir
			},
			expected: true,
		},
		{
			name: "empty root path",
			setupDirs: func(_ *testing.T) (string, string) {
				return "", "/tmp"
			},
			expected: false,
		},
		{
			name: "empty dir path",
			setupDirs: func(t *testing.T) (string, string) {
				root := t.TempDir()
				return root, ""
			},
			expected: false,
		},
		{
			name: "absolute path directory",
			setupDirs: func(t *testing.T) (string, string) {
				root := t.TempDir()
				dir := "/abs/path"
				require.NoError(t, os.MkdirAll(filepath.Join(root, strings.TrimPrefix(dir, "/")), 0o755))
				return root, dir
			},
			expected: true,
		},
		{
			name: "relative traversal escapes root",
			setupDirs: func(t *testing.T) (string, string) {
				root := t.TempDir()
				return root, "../../../etc"
			},
			expected: false,
		},
		{
			name: "directory with no permissions",
			setupDirs: func(t *testing.T) (string, string) {
				root := t.TempDir()
				dir := "/noperm"
				dirPath := filepath.Join(root, strings.TrimPrefix(dir, "/"))
				require.NoError(t, os.MkdirAll(dirPath, 0o755))
				require.NoError(t, os.Chmod(dirPath, 0o000))
				t.Cleanup(func() {
					err := os.Chmod(dirPath, 0o755)
					assert.NoError(t, err)
				})
				return root, dir
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, dir := tt.setupDirs(t)
			result := dirOK(root, dir)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestJavaInjector_AttachOpts(t *testing.T) {
	tests := []struct {
		name     string
		debug    bool
		debugBB  bool
		expected string
	}{
		{
			name:     "no options enabled",
			debug:    false,
			debugBB:  false,
			expected: "",
		},
		{
			name:     "debug only",
			debug:    true,
			debugBB:  false,
			expected: "=debug=true",
		},
		{
			name:     "debugBB only",
			debug:    false,
			debugBB:  true,
			expected: "=debugBB=true",
		},
		{
			name:     "both options enabled",
			debug:    true,
			debugBB:  true,
			expected: "=debug=true,debugBB=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &obi.Config{
				Java: obi.JavaConfig{
					Debug:                tt.debug,
					DebugInstrumentation: tt.debugBB,
				},
			}

			injector := &JavaInjector{
				cfg: cfg,
				log: slog.With("component", "javaagent.Injector"),
			}

			result := injector.attachOpts()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewJavaInjector_Disabled(t *testing.T) {
	injector, err := NewJavaInjector(&obi.Config{
		Java: obi.JavaConfig{
			Enabled: false,
		},
	})

	require.NoError(t, err)
	assert.Nil(t, injector)
}

func TestNewJavaInjector_MissingEmbeddedAgent(t *testing.T) {
	originalEmbeddedBytes := embeddedJavaAgentBytes
	t.Cleanup(func() {
		embeddedJavaAgentBytes = originalEmbeddedBytes
	})

	embeddedJavaAgentBytes = nil

	injector, err := NewJavaInjector(&obi.Config{
		Java: obi.JavaConfig{
			Enabled: true,
		},
	})

	require.Error(t, err)
	assert.Nil(t, injector)
	assert.Contains(t, err.Error(), "embedded OBI java agent artifact is missing from this build")
}

func TestNewJavaInjector_PlaceholderEmbeddedAgent(t *testing.T) {
	originalEmbeddedBytes := embeddedJavaAgentBytes
	t.Cleanup(func() {
		embeddedJavaAgentBytes = originalEmbeddedBytes
	})

	embeddedJavaAgentBytes = []byte(javaAgentEmbedPlaceholder + "\n")

	injector, err := NewJavaInjector(&obi.Config{
		Java: obi.JavaConfig{
			Enabled: true,
		},
	})

	require.Error(t, err)
	assert.Nil(t, injector)
	assert.Contains(t, err.Error(), "embedded OBI java agent artifact is missing from this build")
}

func TestEnsureEmbeddedAgent_ForgotToEmbed(t *testing.T) {
	originalEmbeddedBytes := embeddedJavaAgentBytes
	t.Cleanup(func() {
		embeddedJavaAgentBytes = originalEmbeddedBytes
	})

	embeddedJavaAgentBytes = nil
	err := ensureEmbeddedAgent()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedded OBI java agent artifact is missing from this build")
}

func TestEnsureEmbeddedAgent_PlaceholderBytesError(t *testing.T) {
	originalEmbeddedBytes := embeddedJavaAgentBytes
	t.Cleanup(func() {
		embeddedJavaAgentBytes = originalEmbeddedBytes
	})

	embeddedJavaAgentBytes = []byte(javaAgentEmbedPlaceholder + "\n")
	err := ensureEmbeddedAgent()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedded OBI java agent artifact is missing from this build")
}
