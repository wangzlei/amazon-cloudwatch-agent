// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"testing"

	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
)

func TestOnServiceNameChange(t *testing.T) {
	fi := New(Init{Service: svc.Attrs{UID: svc.UID{Name: "python3"}}})
	var seen []string
	fi.OnServiceNameChange(func() { seen = append(seen, fi.ServiceAttrs().UID.Name) })

	fi.SetUID(svc.UID{Name: "python3", Namespace: "ns"}) // same name: no call
	fi.SetUID(svc.UID{Name: "orders", Namespace: "ns"})  // Kubernetes rename
	fi.SetAutoServiceName("orders")                      // same name: no call
	fi.SetAutoServiceName("billing")

	want := []string{"orders", "billing"}
	if len(seen) != len(want) || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("listener calls = %v, want %v", seen, want)
	}
}
