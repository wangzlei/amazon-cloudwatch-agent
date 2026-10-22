// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package jvmtools // import "go.opentelemetry.io/obi/pkg/internal/jvmtools"

import (
	"path/filepath"
	"strings"

	"go.opentelemetry.io/obi/pkg/internal/procname"
)

// cmdlineServiceName is the Options.NameFromCmdline step of
// ResolveServiceMetadataWithOptions. It returns "" when nothing better than the
// upstream fallback was found.
//
//   - java -jar order-service-1.4.2.jar             -> order-service
//   - java -jar app.jar (Boot launcher Main-Class)  -> simple name of Start-Class
//   - java -cp /app org.springframework.boot.loader.launch.JarLauncher
//     (exploded /app/META-INF/MANIFEST.MF)          -> Implementation-Title, else Start-Class
//   - java [options] com.acme.ProcessService        -> ProcessService
//   - java -m orders/com.acme.Main                  -> "" (Main is generic)
func cmdlineServiceName(args []string, launch JavaLaunch, mainJarPath string, roots []ScanRoot) string {
	if launch.Jar != "" {
		if name := procname.Clean(serviceNameFromJar(launch.Jar)); name != "" {
			return name
		}
		if mainJarPath == "" {
			return ""
		}
		attrs := archiveManifest(mainJarPath)
		if isSpringBootLauncher(attrs["Main-Class"]) {
			return procname.Clean(simpleClassName(attrs["Start-Class"]))
		}
		return ""
	}

	mainClass := javaMainClass(args)
	if mainClass == "" {
		return ""
	}
	if !isSpringBootLauncher(mainClass) {
		return procname.Clean(simpleClassName(mainClass))
	}
	for _, root := range roots {
		var attrs map[string]string
		if root.Directory {
			data, ok := readRegularFile(filepath.Join(root.Path, "META-INF", "MANIFEST.MF"))
			if !ok {
				continue
			}
			attrs = parseManifestAttributes(data)
		} else {
			attrs = archiveManifest(root.Path)
		}
		if name := procname.Clean(attrs["Implementation-Title"]); name != "" {
			return name
		}
		if startClass := attrs["Start-Class"]; startClass != "" {
			return procname.Clean(simpleClassName(startClass))
		}
	}
	return ""
}

func archiveManifest(path string) map[string]string {
	reader, ok := openArchive(path)
	if !ok {
		return nil
	}
	defer reader.Close()
	for _, file := range reader.File {
		if filepath.ToSlash(file.Name) == "META-INF/MANIFEST.MF" {
			if data, ok := readZipEntry(file); ok {
				return parseManifestAttributes(data)
			}
			return nil
		}
	}
	return nil
}

// javaOptionsWithValue are the java launcher options whose value is the next
// argument.
var javaOptionsWithValue = map[string]bool{
	"-cp": true, "-classpath": true, "--class-path": true,
	"-p": true, "--module-path": true, "--upgrade-module-path": true,
	"--add-opens": true, "--add-exports": true, "--add-reads": true,
	"--add-modules": true, "--limit-modules": true, "--patch-module": true,
	"--enable-native-access": true, "--source": true,
}

// javaMainClass returns the main class of a "java [options] MainClass [args]"
// or "java [options] -m module[/MainClass]" launch, or "" for -jar launches.
func javaMainClass(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-jar":
			return ""
		case arg == "-m" || arg == "--module":
			if i+1 < len(args) {
				return moduleMainClass(args[i+1])
			}
			return ""
		case strings.HasPrefix(arg, "--module="):
			return moduleMainClass(strings.TrimPrefix(arg, "--module="))
		case javaOptionsWithValue[arg]:
			i++
		case strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "@"):
			// -D..., -X..., -XX:..., -javaagent:..., --opt=value, @argfile
		default:
			return arg
		}
	}
	return ""
}

// moduleMainClass: "orders/com.acme.Main" -> com.acme.Main; "com.acme.orders" -> com.acme.orders.
func moduleMainClass(module string) string {
	if _, mainClass, ok := strings.Cut(module, "/"); ok {
		return mainClass
	}
	return module
}

// simpleClassName: com.acme.OrdersApplication -> OrdersApplication; Orders.java -> Orders.
func simpleClassName(class string) string {
	if strings.HasSuffix(class, ".java") {
		class = strings.TrimSuffix(filepath.Base(class), ".java")
	}
	if dot := strings.LastIndexByte(class, '.'); dot >= 0 {
		class = class[dot+1:]
	}
	return class
}

func isSpringBootLauncher(class string) bool {
	if !strings.HasPrefix(class, "org.springframework.boot.loader.") {
		return false
	}
	switch simpleClassName(class) {
	case "JarLauncher", "WarLauncher", "PropertiesLauncher":
		return true
	}
	return false
}
