// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package procname // import "go.opentelemetry.io/obi/pkg/internal/procname"

import (
	"go.opentelemetry.io/obi/pkg/appolly/discover/exec"
	ebpfcommon "go.opentelemetry.io/obi/pkg/ebpf/common"
)

// replaceable in tests
var (
	cmdlineForPID  = ebpfcommon.CMDLineForPID
	cwdForPID      = ebpfcommon.CWDForPID
	rootDirForPID  = ebpfcommon.RootDirectoryForPID
	readFileInRoot = readInRoot
)

// Resolve sets a derived automatic service name on fi when it has no name yet
// (no OTEL_SERVICE_NAME / service.name resource attribute, no discovery
// criteria name, no Java resolver result). It returns the name it set, or "".
// It must run before the process is instrumented (traceAttacher), so the name
// is the one the tpinjector propagates in the TCP option.
func Resolve(fi *exec.FileInfo) string {
	if fi == nil || fi.ServiceAttrs().UID.Name != "" {
		return ""
	}
	pid := fi.Pid()
	argv0, args, err := cmdlineForPID(pid)
	if err != nil {
		return ""
	}
	cwd, _ := cwdForPID(pid)
	root := rootDirForPID(pid)
	name := Derive(Process{
		Exe:      fi.ExecutableName(),
		Argv:     append([]string{argv0}, args...),
		Cwd:      cwd,
		ReadFile: func(p string) ([]byte, error) { return readFileInRoot(root, p) },
	})
	if name != "" {
		fi.SetAutoServiceName(name)
	}
	return name
}
