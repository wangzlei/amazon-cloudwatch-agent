// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package procname

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.opentelemetry.io/obi/pkg/appolly/app"
	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
	"go.opentelemetry.io/obi/pkg/appolly/discover/exec"
)

// fakeFS maps absolute in-process paths to file contents.
type fakeFS map[string]string

func (f fakeFS) read(p string) ([]byte, error) {
	if s, ok := f[p]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}

// effectiveName is what OBI ends up with: the derived name, else the
// executable-name fallback of exec.FileInfo.ApplyServiceDefaults.
func effectiveName(p Process) string {
	if n := Derive(p); n != "" {
		return n
	}
	return p.Exe
}

func TestDerive(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		cmd  string // space-separated argv
		cwd  string
		fs   fakeFS
		want string
	}{
		// Python
		{"gunicorn server:app is generic", "python3.12", "python3.12 /usr/local/bin/gunicorn -w 2 -b :8000 server:app", "/srv", nil, "python3.12"},
		{"uvicorn orders.main:app -> first non-generic module component", "python3", "python -m uvicorn orders.main:app", "/srv", nil, "orders"},
		{"manage.py runserver is generic", "python3", "python manage.py runserver", "/srv", nil, "python3"},
		{"plain script", "python3", "python /srv/billing_worker.py", "/", nil, "billing_worker"},
		{"-m module", "python3", "python3 -u -m billing.worker", "/", nil, "worker"},
		{"-m module with generic last component", "python3", "python3 -m billing.__main__", "/", nil, "billing"},
		{"gunicorn bind with host:port is not the target", "python3", "python3 /usr/local/bin/gunicorn -b 0.0.0.0:8000 payments.wsgi:application", "/", nil, "payments"},
		{"gunicorn setproctitle", "python3.12", "gunicorn: worker [inventory:app]", "/", nil, "inventory"},
		{"celery -A", "python3", "python3 /usr/local/bin/celery -A tasks.celery_app worker", "/", nil, "celery_app"},
		{"daphne asgi", "python3", "python3 /usr/local/bin/daphne -b 0.0.0.0 shop.asgi:application", "/", nil, "shop"},
		{"python -c stays fallback", "python3", "python3 -c print(1)", "/", nil, "python3"},
		{"pyproject wins over script", "python3", "python3 main.py", "/srv/app", fakeFS{"/srv/app/pyproject.toml": "[build-system]\nname = \"x\"\n[project]\nname = \"ledger-api\"\nversion = \"1.0\"\n"}, "ledger-api"},
		{"script version stripped", "python3", "python3 /srv/report-gen-2.1.py", "/", nil, "report-gen"},

		// Node.js
		{"node server.js is generic", "node", "node server.js", "/srv", nil, "node"},
		{"package.json name with npm scope", "node", "node dist/index.js", "/srv/orders", fakeFS{"/srv/orders/package.json": `{"name":"@acme/orders-api","version":"1.0.0"}`}, "orders-api"},
		{"package.json next to the script", "node", "node /opt/svc/lib/main.js", "/", fakeFS{"/opt/svc/lib/package.json": `{"name":"svc-worker"}`}, "svc-worker"},
		{"node options skipped", "node", "node --require ./otel.js -r dotenv/config --inspect=0.0.0.0:9229 checkout.mjs", "/srv", nil, "checkout"},
		{"npm start uses cwd package.json", "node", "node /usr/local/lib/node_modules/npm/bin/npm-cli.js start", "/srv/cart", fakeFS{"/srv/cart/package.json": `{"name":"cart"}`, "/usr/local/lib/node_modules/npm/package.json": `{"name":"npm"}`}, "cart"},
		{"tool under node_modules without package.json", "node", "node node_modules/.bin/next start", "/srv", nil, "node"},
		{"node -e stays fallback", "node", "node -e 1", "/", nil, "node"},

		// .NET, Ruby, PHP
		{"dotnet assembly", "dotnet", "dotnet Orders.Api.dll", "/app", nil, "Orders.Api"},
		{"dotnet exec with host option", "dotnet", "dotnet exec --runtimeconfig x.json /app/Billing.dll", "/app", nil, "Billing"},
		{"ruby script", "ruby", "ruby -I lib /srv/notifier.rb", "/", nil, "notifier"},
		{"php script", "php8.2", "php -d memory_limit=1G worker.php", "/", nil, "worker"},

		// Native and unknown executables keep the executable name.
		{"native binary", "ping-app", "/ping-app", "/", nil, "ping-app"},
		{"native binary with args", "envoy", "envoy -c /etc/envoy.yaml", "/", nil, "envoy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Process{Exe: tt.exe, Argv: strings.Fields(tt.cmd), Cwd: tt.cwd}
			if strings.HasPrefix(tt.cmd, "gunicorn: ") {
				p.Argv = []string{tt.cmd} // setproctitle: one NUL-free string
			}
			if tt.fs != nil {
				p.ReadFile = tt.fs.read
			}
			assert.Equal(t, tt.want, effectiveName(p), "cmdline %q", tt.cmd)
		})
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"order-service-1.4.2":          "order-service",
		"order-service-1.4.2-SNAPSHOT": "order-service",
		"orders api":                   "orders-api",
		"Main":                         "",
		"APPLICATION":                  "",
		"__main__":                     "",
		"":                             "",
		"1.2.3":                        "1.2.3",
		strings.Repeat("a", 70):        strings.Repeat("a", MaxLen),
	} {
		assert.Equal(t, want, Clean(in), "Clean(%q)", in)
	}
}

func mockProc(t *testing.T, argv []string, cwd string, fs fakeFS) {
	t.Helper()
	oldCmd, oldCwd, oldRoot, oldRead := cmdlineForPID, cwdForPID, rootDirForPID, readFileInRoot
	cmdlineForPID = func(app.PID) (string, []string, error) {
		if len(argv) == 0 {
			return "", nil, errors.New("gone")
		}
		return argv[0], argv[1:], nil
	}
	cwdForPID = func(app.PID) (string, error) { return cwd, nil }
	rootDirForPID = func(app.PID) string { return "/proc/1/root" }
	readFileInRoot = func(_, p string) ([]byte, error) { return fs.read(p) }
	t.Cleanup(func() { cmdlineForPID, cwdForPID, rootDirForPID, readFileInRoot = oldCmd, oldCwd, oldRoot, oldRead })
}

func TestResolve(t *testing.T) {
	mockProc(t, []string{"node", "index.js"}, "/srv/backend", fakeFS{"/srv/backend/package.json": `{"name":"orders-backend"}`})

	fi := exec.New(exec.Init{Pid: 42, CmdExePath: "/usr/bin/node"})
	assert.Equal(t, "orders-backend", Resolve(fi))
	attrs := fi.ServiceAttrs()
	assert.Equal(t, "orders-backend", attrs.UID.Name)
	assert.True(t, attrs.AutoName(), "derived name must stay overridable by Kubernetes metadata")
}

func TestResolveKeepsEnvName(t *testing.T) {
	mockProc(t, []string{"node", "index.js"}, "/srv/backend", fakeFS{"/srv/backend/package.json": `{"name":"orders-backend"}`})

	fi := exec.New(exec.Init{Pid: 42, CmdExePath: "/usr/bin/node"})
	fi.ApplyEnvVariables(map[string]string{"OTEL_SERVICE_NAME": "checkout"})
	assert.Empty(t, Resolve(fi))
	assert.Equal(t, "checkout", fi.ServiceAttrs().UID.Name)

	fi = exec.New(exec.Init{Pid: 43, CmdExePath: "/usr/bin/node"})
	fi.ApplyEnvVariables(map[string]string{"OTEL_RESOURCE_ATTRIBUTES": "service.name=cart,deployment.environment=prod"})
	assert.Empty(t, Resolve(fi))
	assert.Equal(t, "cart", fi.ServiceAttrs().UID.Name)

	// a name from the discovery criteria is kept too
	fi = exec.New(exec.Init{Pid: 44, CmdExePath: "/usr/bin/node", Service: svc.Attrs{UID: svc.UID{Name: "from-criteria"}}})
	assert.Empty(t, Resolve(fi))
	assert.Equal(t, "from-criteria", fi.ServiceAttrs().UID.Name)
}

func TestResolveNativeKeepsFallback(t *testing.T) {
	mockProc(t, []string{"/ping-app"}, "/", nil)
	fi := exec.New(exec.Init{Pid: 42, CmdExePath: "/ping-app"})
	assert.Empty(t, Resolve(fi))
	fi.ApplyServiceDefaults(svc.InstrumentableGolang)
	assert.Equal(t, "ping-app", fi.ServiceAttrs().UID.Name)
}
