// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package javaagent // import "go.opentelemetry.io/obi/pkg/internal/java"

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"go.opentelemetry.io/obi/pkg/appolly/app"
	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
	"go.opentelemetry.io/obi/pkg/ebpf"
	ebpfcommon "go.opentelemetry.io/obi/pkg/ebpf/common"
	"go.opentelemetry.io/obi/pkg/internal/jvmtools/jvm"
	"go.opentelemetry.io/obi/pkg/obi"
)

const (
	ObiJavaAgentFileName      = "obi-java-agent.jar"
	javaAgentEmbedPlaceholder = "OBI_JAVA_AGENT_PLACEHOLDER"
)

//go:embed embedded/obi-java-agent.jar
var embeddedJavaAgentBytes []byte

type JavaInjectError struct {
	Message string
}

func (e *JavaInjectError) Error() string {
	return e.Message
}

type JavaInjector struct {
	log             *slog.Logger
	cfg             *obi.Config
	currentAttachID int64
	mu              sync.Mutex
}

func NewJavaInjector(cfg *obi.Config) (*JavaInjector, error) {
	if !cfg.Java.Enabled {
		return nil, nil
	}
	if err := ensureEmbeddedAgent(); err != nil {
		return nil, err
	}

	return &JavaInjector{
		cfg:             cfg,
		log:             slog.With("component", "javaagent.Injector"),
		currentAttachID: 0,
	}, nil
}

// tempDirs are the directories, inside the target's root, where the agent jar
// is written, in order of preference. The target's own TMPDIR is deliberately
// not used: it comes from the environment of the (untrusted) target process.
var tempDirs = []string{"/tmp", "/var/tmp"}

// openDirInRoot opens the absolute directory dir inside root (/proc/<pid>/root)
// and returns an O_PATH directory fd. openat2 with RESOLVE_IN_ROOT and
// RESOLVE_NO_SYMLINKS refuses any symlink on the path and keeps ".." inside
// root, so a file system controlled by the target process cannot redirect the
// jar write to a host directory. Kernels without openat2 (< 5.6) get an error,
// and the agent is not injected.
func openDirInRoot(root, dir string) (int, error) {
	if root == "" || !filepath.IsAbs(dir) {
		return -1, fmt.Errorf("invalid directory %q in root %q", dir, root)
	}
	rootFD, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(rootFD)

	rel := strings.TrimPrefix(filepath.Clean(dir), "/")
	if rel == "" {
		rel = "."
	}
	return unix.Openat2(rootFD, rel, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_IN_ROOT | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
}

func dirOK(root, dir string) bool {
	fd, err := openDirInRoot(root, dir)
	if err != nil {
		return false
	}
	unix.Close(fd)
	return true
}

// findTempDir returns the first usable directory of tempDirs inside root, and an
// O_PATH fd for it that the caller must close.
func findTempDir(root string) (string, int, error) {
	for _, dir := range tempDirs {
		if fd, err := openDirInRoot(root, dir); err == nil {
			return dir, fd, nil
		}
	}
	return "", -1, errors.New("couldn't find suitable temp directory for injection")
}

func (i *JavaInjector) nextAttachID() int64 {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.currentAttachID++
	return i.currentAttachID
}

func (i *JavaInjector) runIfCurrentAttach(
	attachID int64,
	fn func() error,
) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.currentAttachID != attachID {
		return nil
	}

	return fn()
}

func (i *JavaInjector) NewExecutable(ie *ebpf.Instrumentable) error {
	if ie.Type != svc.InstrumentableJava {
		return nil
	}

	attachID := i.nextAttachID()

	ctx, cancel := context.WithTimeout(context.Background(), i.cfg.Java.Timeout)
	defer cancel()

	// Channel to receive the result
	type result struct {
		attached bool
		err      error
	}

	resultChan := make(chan result, 1)

	attacher := jvm.NewJAttacher(i.log, attachID, i.runIfCurrentAttach)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// We need to call cleanup here even though init may not have
	// happened, to ensure the case when the JVM never responds and
	// we've terminated with a timeout. Cleanup() is idempotent.
	defer func() {
		if err := attacher.Terminate(); err != nil {
			i.log.Warn("error on JVM attach cleanup", "error", err)
		}
	}()

	// Run the attach procedure in a goroutine, so that we can terminate on stuck attach
	go func() {
		defer func() {
			if r := recover(); r != nil {
				resultChan <- result{err: &JavaInjectError{Message: "attach failed"}}
			}
		}()

		ok, jdk8 := i.verifyJVMVersion(ctx, attacher, ie.FileInfo.Pid())
		if !ok {
			resultChan <- result{err: &JavaInjectError{Message: "unsupported Java version for OpenTelemetry eBPF instrumentation"}}
			return
		}

		var loaded bool
		var err error
		if jdk8 {
			loaded, err = i.jdkAgentAlreadyLoadedHotspot8(ctx, attacher, ie.FileInfo.Pid())
		} else {
			loaded, err = i.jdkAgentAlreadyLoaded(ctx, attacher, ie.FileInfo.Pid())
		}

		if err != nil {
			resultChan <- result{err: err}
			return
		}

		if loaded {
			i.log.Info("OpenTelemetry eBPF Java Agent already loaded, not reloading")
			resultChan <- result{attached: false}
			return
		}

		i.log.Info("injecting OpenTelemetry eBPF instrumentation for Java process", "pid", ie.FileInfo.Pid())

		agentPath, err := i.copyAgent(ie)
		if err != nil {
			i.log.Error("failed to extract java agent", "pid", ie.FileInfo.Pid(), "error", err)
			resultChan <- result{err: err}
			return
		}

		if err = i.attachJDKAgent(ctx, attacher, ie.FileInfo.Pid(), agentPath); err != nil {
			i.log.Error("couldn't attach OpenTelemetry eBPF Java Agent", "pid", ie.FileInfo.Pid(), "path", agentPath, "error", err)
			resultChan <- result{err: err}
			return
		}

		resultChan <- result{attached: true}
	}()

	// Wait for either completion or timeout
	select {
	case result := <-resultChan:
		return result.err
	case <-ctx.Done():
		i.log.Warn("java attach timed out", "timeout", i.cfg.Java.Timeout, "pid", ie.FileInfo.Pid())
		return &JavaInjectError{Message: "java attach timed out"}
	}
}

func ensureEmbeddedAgent() error {
	if len(embeddedJavaAgentBytes) == 0 || strings.TrimSpace(string(embeddedJavaAgentBytes)) == javaAgentEmbedPlaceholder {
		return errors.New("embedded OBI java agent artifact is missing from this build; Java TLS telemetry generation will be disabled")
	}

	return nil
}

// to be changed in tests
var rootDirForPID func(app.PID) string = ebpfcommon.RootDirectoryForPID

// copyAgent writes the embedded agent jar into a temp directory of the target's
// root and returns its path as the target sees it. Every file operation is
// relative to the directory fd from findTempDir (no path is resolved again): the
// jar is created with O_EXCL|O_NOFOLLOW under a random name and renamed over
// obi-java-agent.jar, which replaces a symlink there instead of following it.
func (i *JavaInjector) copyAgent(ie *ebpf.Instrumentable) (string, error) {
	root := rootDirForPID(ie.FileInfo.Pid())
	tempDir, dirFD, err := findTempDir(root)
	if err != nil {
		return "", fmt.Errorf("error accessing temp directory: %w", err)
	}
	defer unix.Close(dirFD)

	i.log.Info("found injection directory for process", "pid", ie.FileInfo.Pid(), "path", filepath.Join(root, tempDir))

	var (
		f       *os.File
		tmpName string
	)
	for attempt := 0; f == nil; attempt++ {
		tmpName = fmt.Sprintf("%s.tmp-%016x", ObiJavaAgentFileName, rand.Uint64())
		fd, err := unix.Openat(dirFD, tmpName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
		if errors.Is(err, unix.EEXIST) && attempt < 10 {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("unable to create target OBI java agent: %w", err)
		}
		f = os.NewFile(uintptr(fd), tmpName)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = unix.Unlinkat(dirFD, tmpName, 0)
		}
	}()

	if _, err = f.ReadFrom(bytes.NewReader(embeddedJavaAgentBytes)); err != nil {
		f.Close()
		return "", fmt.Errorf("error writing java agent to target location: %w", err)
	}

	// the umask may have removed bits from the mode given to openat
	if err = f.Chmod(0o644); err != nil {
		f.Close()
		return "", fmt.Errorf("error setting permissions on target OBI java agent: %w", err)
	}

	if err = f.Close(); err != nil {
		return "", fmt.Errorf("error closing target OBI java agent: %w", err)
	}

	if err = unix.Renameat(dirFD, tmpName, dirFD, ObiJavaAgentFileName); err != nil {
		return "", fmt.Errorf("unable to move target OBI java agent into place: %w", err)
	}
	cleanup = false

	return filepath.Join(tempDir, ObiJavaAgentFileName), nil
}

func returnCodeLine(line string) (bool, error) {
	if strings.Contains(line, "return code: 0") || strings.Contains(line, "ATTACH_ACK") {
		return true, nil
	} else if strings.Contains(line, "return code:") {
		return true, fmt.Errorf("error executing command for the JVM %s", line)
	}

	return false, nil
}

func (i *JavaInjector) attachOpts() string {
	var opts []string
	if i.cfg.Java.Debug {
		opts = append(opts, "debug=true")
	}
	if i.cfg.Java.DebugInstrumentation {
		opts = append(opts, "debugBB=true")
	}

	if len(opts) == 0 {
		return ""
	}

	return "=" + strings.Join(opts, ",")
}

func (i *JavaInjector) attachJDKAgent(ctx context.Context, attacher *jvm.JAttacher, pid app.PID, path string) error {
	attacher.Init()

	defer func() {
		if err := attacher.Cleanup(); err != nil {
			slog.Warn("error on JVM attach cleanup", "error", err)
		}
	}()
	out, err := attacher.Attach(ctx, int(pid), []string{"load", "instrument", "false", path + i.attachOpts()}, false)
	if err != nil {
		i.log.Error("error executing command for the JVM", "pid", pid, "error", err)
		return err
	}

	defer out.Close()

	reader := bufio.NewReader(out)
	buf := bytes.Buffer{}
	for {
		b, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF { // hotspot terminates with EOF
				_, err := returnCodeLine(buf.String())
				if err != nil {
					return err
				}
				break
			}
			return fmt.Errorf("error reading line %w", err)
		}

		buf.WriteByte(b)
		if b == '\n' {
			if end, err := returnCodeLine(buf.String()); end {
				return err
			}

			buf.Reset()
		} else if b == 0 { // j9 terminates with 0
			if end, err := returnCodeLine(buf.String()); end {
				return err
			}
			break
		}
	}

	return nil
}

func (i *JavaInjector) jdkAgentAlreadyLoaded(ctx context.Context, attacher *jvm.JAttacher, pid app.PID) (bool, error) {
	attacher.Init()

	defer func() {
		if err := attacher.Cleanup(); err != nil {
			slog.Warn("error on JVM attach cleanup", "error", err)
		}
	}()
	// OpenJ9 doesn't support listing loaded classes
	out, err := attacher.Attach(ctx, int(pid), []string{"jcmd", "VM.class_hierarchy"}, true)
	if err != nil {
		i.log.Error("error executing command for the JVM", "pid", pid, "error", err)
		return false, err
	}

	if out == nil {
		return false, nil
	}

	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		s := scanner.Text()
		// We check for io.opentelemetry.obi.java.Agent/0x<address>
		if strings.Contains(s, "io.opentelemetry.obi.java.Agent/0x") {
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		i.log.Error("error reading JVM command output", "pid", pid, "error", err)
		return false, err
	}

	return false, nil
}

// Hotspot version 8 doesn't support VM.class_hierarchy, we use GC.class_histogram and look for the class itself
// without the address
func (i *JavaInjector) jdkAgentAlreadyLoadedHotspot8(ctx context.Context, attacher *jvm.JAttacher, pid app.PID) (bool, error) {
	attacher.Init()

	defer func() {
		if err := attacher.Cleanup(); err != nil {
			slog.Warn("error on JVM attach cleanup", "error", err)
		}
	}()
	// OpenJ9 doesn't support listing loaded classes
	out, err := attacher.Attach(ctx, int(pid), []string{"jcmd", "GC.class_histogram"}, true)
	if err != nil {
		i.log.Error("error executing command for the JVM", "pid", pid, "error", err)
		return false, err
	}

	if out == nil {
		return false, nil
	}

	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		s := scanner.Text()
		// We check for io.opentelemetry.obi.java.Agent
		if strings.Contains(s, "io.opentelemetry.obi.java.Agent") {
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		i.log.Error("error reading JVM command output", "pid", pid, "error", err)
		return false, err
	}

	return false, nil
}

func (i *JavaInjector) verifyJVMVersion(ctx context.Context, attacher *jvm.JAttacher, pid app.PID) (bool, bool) {
	attacher.Init()

	defer func() {
		if err := attacher.Cleanup(); err != nil {
			slog.Warn("error on JVM attach cleanup", "error", err)
		}
	}()
	// OpenJ9 doesn't support VM.version command
	out, err := attacher.Attach(ctx, int(pid), []string{"jcmd", "VM.version"}, true)
	if err != nil {
		i.log.Error("error executing command for the JVM", "pid", pid, "error", err)
		return false, false
	}

	if out == nil {
		return true, false
	}

	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "JDK ") {
			// JDK 8 is special, failing to properly detect it can cause errors in applications if they are
			// loaded more than once
			return !strings.HasPrefix(line, "JDK 28"), strings.HasPrefix(line, "JDK 8")
		}
	}
	if err := scanner.Err(); err != nil {
		i.log.Error("error reading from scanner", "error", err)
	}

	return false, false
}
