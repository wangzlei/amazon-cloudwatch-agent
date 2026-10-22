// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package jvmtools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
)

func TestResolveServiceNameFromCmdline(t *testing.T) {
	tests := []struct {
		name  string
		cmd   string            // java arguments (after the executable)
		files map[string]string // files under the process root (cwd is /app)
		jars  map[string]map[string]string
		env   map[string]string
		want  string // "" = no name set, the "java" fallback applies
	}{
		{name: "-cp /app Main is generic", cmd: "-XX:+UseSerialGC -cp /app Main", want: ""},
		{name: "plain main class", cmd: "ProcessService", want: "ProcessService"},
		{name: "main class options skipped", cmd: "-Xmx512m -Dfoo=bar -javaagent:/otel.jar --add-opens java.base/java.lang=ALL-UNNAMED -cp /app:/lib/* com.acme.billing.BillingWorker --port 8080", want: "BillingWorker"},
		{name: "module main class", cmd: "-p /mods -m orders/com.acme.orders.OrdersMain", want: "OrdersMain"},
		{name: "module with generic main class", cmd: "--module-path /mods -m orders/com.acme.Main", want: ""},
		{name: "jar version stripped", cmd: "-jar /app/order-service-1.4.2.jar", want: "order-service"},
		{name: "jar SNAPSHOT version stripped", cmd: "-jar /app/order-service-1.4.2-SNAPSHOT.jar", want: "order-service"},
		{
			name:  "exploded Spring Boot JarLauncher reads Start-Class",
			cmd:   "-cp /app org.springframework.boot.loader.launch.JarLauncher",
			files: map[string]string{"app/META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\nMain-Class: org.springframework.boot.loader.launch.JarLauncher\r\nStart-Class: com.acme.OrdersApplication\r\n\r\n"},
			want:  "OrdersApplication",
		},
		{
			name: "app.jar with Boot launcher reads Start-Class",
			cmd:  "-jar /app/app.jar",
			jars: map[string]map[string]string{"app/app.jar": {"META-INF/MANIFEST.MF": "Main-Class: org.springframework.boot.loader.JarLauncher\r\nStart-Class: com.acme.inventory.InventoryApplication\r\n\r\n"}},
			want: "InventoryApplication",
		},
		{
			name: "generic app.jar without launcher keeps the upstream jar name",
			cmd:  "-jar /app/app.jar",
			jars: map[string]map[string]string{"app/app.jar": {"META-INF/MANIFEST.MF": "Main-Class: com.acme.Main\r\n\r\n"}},
			want: "app",
		},
		{name: "spring.application.name still wins", cmd: "-Dspring.application.name=payments -jar /app/order-service-1.4.2.jar", want: "payments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for p, content := range tt.files {
				require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(root, p), []byte(content), 0o644))
			}
			for p, entries := range tt.jars {
				writeArchive(t, filepath.Join(root, p), entries)
			}
			fi := mockJavaProcess(t, root, strings.Fields(tt.cmd), tt.env)
			require.NoError(t, ResolveServiceMetadataWithOptions(fi, Options{NameFromCmdline: true}))
			assert.Equal(t, tt.want, fi.ServiceAttrs().UID.Name)
		})
	}
}

func TestResolveServiceNameFromCmdlineDisabledKeepsUpstream(t *testing.T) {
	fi := mockJavaProcess(t, t.TempDir(), []string{"-jar", "/app/order-service-1.4.2.jar"}, nil)
	require.NoError(t, ResolveServiceMetadata(fi))
	assert.Equal(t, "order-service-1.4.2", fi.ServiceAttrs().UID.Name)

	fi = mockJavaProcess(t, t.TempDir(), []string{"ProcessService"}, nil)
	require.NoError(t, ResolveServiceMetadata(fi))
	assert.Empty(t, fi.ServiceAttrs().UID.Name)
}

func TestResolveServiceNameFromCmdlineKeepsEnvName(t *testing.T) {
	fi := mockJavaProcess(t, t.TempDir(), []string{"-jar", "/app/order-service-1.4.2.jar"}, nil)
	fi.SetUID(svc.UID{Name: "from-env"})
	require.NoError(t, ResolveServiceMetadataWithOptions(fi, Options{NameFromCmdline: true}))
	assert.Equal(t, "from-env", fi.ServiceAttrs().UID.Name)
}
