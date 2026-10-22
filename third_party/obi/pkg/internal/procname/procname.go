// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package procname derives a service name for an interpreted process
// (Python, Node.js, .NET, Ruby, PHP) from its project metadata or its command
// line, for processes that set neither OTEL_SERVICE_NAME nor service.name in
// OTEL_RESOURCE_ATTRIBUTES. Without it, every such process is named after its
// interpreter (python3, node, dotnet, ...).
//
// Enabled by discovery.service_name_from_cmdline. The name is stored as an
// automatic name, so Kubernetes metadata still overrides it. Java has its own
// resolver in pkg/internal/jvmtools, which uses Clean from this package.
package procname // import "go.opentelemetry.io/obi/pkg/internal/procname"

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// MaxLen caps a derived name (a DNS label is at most 63 characters).
const MaxLen = 63

// maxFileBytes bounds the project files read from a process (package.json,
// pyproject.toml).
const maxFileBytes = 1 << 20

// genericNames carry no service identity; a derived name equal to one of them
// (case-insensitive) is discarded so the executable-name fallback is kept.
var genericNames = map[string]struct{}{}

func init() {
	for _, n := range []string{
		"main", "app", "index", "server", "run", "start", "wsgi", "asgi", "manage",
		"application", "dist", "src", "bin", "lib", "build",
	} {
		genericNames[n] = struct{}{}
	}
}

// GenericNames returns the names that are never used as a derived name.
func GenericNames() []string {
	out := make([]string, 0, len(genericNames))
	for n := range genericNames {
		out = append(out, n)
	}
	return out
}

// IsGeneric reports whether name carries no service identity.
func IsGeneric(name string) bool {
	_, ok := genericNames[strings.ToLower(name)]
	return ok
}

var versionSuffix = regexp.MustCompile(`-\d+(\.\d+)*(-SNAPSHOT)?$`)

// Clean turns a raw candidate into a service name: characters outside
// [A-Za-z0-9._-] become '-', a trailing version (-1.4.2, -1.4.2-SNAPSHOT) is
// removed, leading and trailing separators are trimmed and the result is capped
// at MaxLen. It returns "" when nothing meaningful is left (empty or generic).
func Clean(name string) string {
	b := []byte(name)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			b[i] = '-'
		}
	}
	name = versionSuffix.ReplaceAllString(string(b), "")
	name = strings.Trim(name, "-._")
	if len(name) > MaxLen {
		name = strings.TrimRight(name[:MaxLen], "-._")
	}
	if name == "" || IsGeneric(name) {
		return ""
	}
	return name
}

// Process is the input of Derive. Paths are as seen by the process (its mount
// namespace); ReadFile resolves them inside the process root.
type Process struct {
	// Exe is the basename of the executable (/proc/<pid>/exe), e.g. python3.12.
	Exe string
	// Argv is the full command line, Argv[0] included.
	Argv []string
	// Cwd is the working directory of the process.
	Cwd string
	// ReadFile reads an absolute path inside the process root. May be nil.
	ReadFile func(path string) ([]byte, error)
}

var (
	pythonExe = regexp.MustCompile(`^python(\d+(\.\d+)?)?[a-z]?$`)
	nodeExe   = regexp.MustCompile(`^(node|nodejs)$`)
	rubyExe   = regexp.MustCompile(`^ruby(\d+(\.\d+)*)?$`)
	phpExe    = regexp.MustCompile(`^php(\d+(\.\d+)*)?$`)
)

// Derive returns a service name for p, or "" when the executable is not a
// known interpreter or nothing better than the executable name was found.
// Project metadata wins over the command line.
func Derive(p Process) string {
	argv := p.Argv
	// setproctitle (gunicorn: "gunicorn: worker [server:app]") rewrites the
	// command line as one space-separated string.
	if len(argv) == 1 && strings.ContainsRune(argv[0], ' ') {
		argv = strings.Fields(argv[0])
	}
	if len(argv) == 0 {
		return ""
	}
	args := argv[1:]
	switch {
	case pythonExe.MatchString(p.Exe):
		if n := pyprojectName(p); n != "" {
			return n
		}
		if w := pythonWrapper(path.Base(strings.TrimSuffix(argv[0], ":"))); w != "" {
			return fromPythonWrapper(w, args)
		}
		return fromPython(args)
	case nodeExe.MatchString(p.Exe):
		return fromNode(p, args)
	case p.Exe == "dotnet":
		return fromDotnet(args)
	case rubyExe.MatchString(p.Exe):
		return Clean(trimExt(path.Base(firstScript(args, rubyValueOpts, rubyStopOpts)), ".rb"))
	case phpExe.MatchString(p.Exe):
		return Clean(trimExt(path.Base(firstScript(args, phpValueOpts, phpStopOpts)), ".php"))
	}
	return ""
}

func trimExt(name string, exts ...string) string {
	for _, e := range exts {
		if strings.HasSuffix(name, e) {
			return strings.TrimSuffix(name, e)
		}
	}
	return name
}

// firstScript returns the first non-option argument, skipping the values of
// the options in valueOpts; it returns "" at any option in stopOpts (inline
// code, interactive mode, ...).
func firstScript(args []string, valueOpts, stopOpts map[string]bool) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case stopOpts[a]:
			return ""
		case a == "--":
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		case valueOpts[a]:
			i++
		case a == "-":
			return ""
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	return ""
}

func set(s ...string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

// ---------- Python ----------

var (
	pyValueOpts = set("-X", "-W", "--check-hash-based-pycs")
	pyStopOpts  = set("-c", "-i")
	// wrappers that serve a WSGI/ASGI (or Celery) application given as a target.
	pyWrappers = set("gunicorn", "uvicorn", "hypercorn", "waitress", "waitress-serve", "celery", "daphne")
	// module:attr, e.g. orders.main:app or app:create_app()
	appTarget = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.]*):[A-Za-z_][A-Za-z0-9_.]*(\(.*\))?$`)
)

func pythonWrapper(name string) string {
	if pyWrappers[name] {
		return name
	}
	return ""
}

func fromPython(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case pyStopOpts[a] || a == "-":
			return ""
		case a == "-m" || (strings.HasPrefix(a, "-m") && len(a) > 2 && !strings.HasPrefix(a, "--")):
			mod := strings.TrimPrefix(a, "-m")
			rest := args[i+1:]
			if mod == "" {
				if len(rest) == 0 {
					return ""
				}
				mod, rest = rest[0], rest[1:]
			}
			if w := pythonWrapper(strings.SplitN(mod, ".", 2)[0]); w != "" {
				return fromPythonWrapper(w, rest)
			}
			return dotted(mod)
		case pyValueOpts[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			base := trimExt(path.Base(a), ".py", ".pyw", ".pyc", ".pyz")
			if w := pythonWrapper(base); w != "" {
				return fromPythonWrapper(w, args[i+1:])
			}
			return Clean(base)
		}
	}
	return ""
}

// dotted returns the last non-generic component of a dotted module path:
// orders.main -> orders, billing.worker -> worker.
func dotted(mod string) string {
	parts := strings.Split(mod, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		if n := Clean(parts[i]); n != "" {
			return n
		}
	}
	return ""
}

func fromPythonWrapper(wrapper string, args []string) string {
	if wrapper == "celery" {
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case (a == "-A" || a == "--app") && i+1 < len(args):
				return dotted(strings.SplitN(args[i+1], ":", 2)[0])
			case strings.HasPrefix(a, "--app="):
				return dotted(strings.SplitN(strings.TrimPrefix(a, "--app="), ":", 2)[0])
			case strings.HasPrefix(a, "-A") && len(a) > 2:
				return dotted(strings.SplitN(a[2:], ":", 2)[0])
			}
		}
		return ""
	}
	// The application target is the last module:attr argument; option values
	// such as -b :8000 or -b 0.0.0.0:8000 do not match the pattern.
	for i := len(args) - 1; i >= 0; i-- {
		a := strings.Trim(args[i], "[]")
		if m := appTarget.FindStringSubmatch(a); m != nil {
			return dotted(m[1])
		}
	}
	return ""
}

// pyprojectName reads [project].name (or [tool.poetry].name) from
// pyproject.toml in the working directory.
func pyprojectName(p Process) string {
	if p.ReadFile == nil || p.Cwd == "" {
		return ""
	}
	data, err := p.ReadFile(path.Join(p.Cwd, "pyproject.toml"))
	if err != nil || len(data) > maxFileBytes {
		return ""
	}
	inTable := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			t := strings.TrimSpace(strings.Trim(line, "[]"))
			inTable = t == "project" || t == "tool.poetry"
			continue
		}
		if !inTable {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "name" {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
			if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
				return Clean(v[1 : end+1])
			}
		}
		return ""
	}
	return ""
}

// ---------- Node.js ----------

var (
	nodeValueOpts = set("-r", "--require", "--import", "--loader", "--experimental-loader",
		"--env-file", "--title", "--input-type", "--conditions", "-C", "--watch-path",
		"--inspect-port", "--debug-port", "--max-http-header-size", "--stack-size",
		"--max-old-space-size", "--openssl-config", "--icu-data-dir", "--disable-warning")
	nodeStopOpts = set("-e", "--eval", "-p", "--print", "-i", "--interactive", "-c", "--check",
		"--test", "-v", "--version", "-h", "--help")
)

func fromNode(p Process, args []string) string {
	script := firstScript(args, nodeValueOpts, nodeStopOpts)
	if script == "" {
		return ""
	}
	if n := packageJSONName(p, script); n != "" {
		return n
	}
	if strings.Contains(script, "node_modules/") {
		// a tool started from node_modules (npm, next, ts-node, ...): its file
		// name is not the service; only the project's package.json is.
		return ""
	}
	return Clean(trimExt(path.Base(script), ".js", ".mjs", ".cjs", ".ts"))
}

// packageJSONName returns the npm package name (scope removed) of the nearest
// package.json, searched from the script's directory up to the working
// directory. For a script under node_modules only the working directory is
// searched, so npm's own package.json is never used.
func packageJSONName(p Process, script string) string {
	if p.ReadFile == nil {
		return ""
	}
	abs := script
	if !path.IsAbs(abs) {
		if p.Cwd == "" {
			return ""
		}
		abs = path.Join(p.Cwd, abs)
	}
	var dirs []string
	if strings.Contains(abs, "/node_modules/") {
		if p.Cwd != "" {
			dirs = []string{p.Cwd}
		}
	} else {
		dir := path.Dir(abs)
		if script == "." || strings.HasSuffix(script, "/") {
			dir = abs // node <dir>: the directory itself is the package
		}
		for i := 0; i < 6 && dir != "/" && dir != "."; i++ {
			dirs = append(dirs, dir)
			if dir == p.Cwd {
				break
			}
			dir = path.Dir(dir)
		}
	}
	for _, d := range dirs {
		data, err := p.ReadFile(path.Join(d, "package.json"))
		if err != nil || len(data) > maxFileBytes {
			continue
		}
		var pkg struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &pkg) != nil || pkg.Name == "" {
			continue
		}
		name := pkg.Name
		if strings.HasPrefix(name, "@") {
			if _, after, ok := strings.Cut(name, "/"); ok {
				name = after
			}
		}
		return Clean(name)
	}
	return ""
}

// ---------- .NET, Ruby, PHP ----------

var (
	dotnetValueOpts = set("--runtimeconfig", "--depsfile", "--additionalprobingpath",
		"--additional-deps", "--fx-version", "--roll-forward")
	rubyValueOpts = set("-I", "-r", "-C", "-E", "--encoding", "-x")
	rubyStopOpts  = set("-e", "-v", "--version", "-h", "--help")
	phpValueOpts  = set("-c", "-d", "-t", "-z")
	phpStopOpts   = set("-r", "-S", "-a", "-i", "-v", "-h", "-m")
)

// fromDotnet handles "dotnet [exec] [host options] X.dll": the assembly name.
func fromDotnet(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case dotnetValueOpts[a]:
			i++
		case strings.HasSuffix(strings.ToLower(a), ".dll"):
			return Clean(a[strings.LastIndexByte(a, '/')+1 : len(a)-len(".dll")])
		}
	}
	return ""
}
