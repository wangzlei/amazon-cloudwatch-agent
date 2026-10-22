// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package meta // import "go.opentelemetry.io/obi/pkg/appolly/meta"

import (
	"context"

	"go.opentelemetry.io/contrib/detectors/azure/azurevm"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

// ADOT-OBI (patch 99-pin-to-cwagent): the CloudWatch Agent's otel v1.44.0 line
// forces azurevm v0.16.0, which predates azurevm.NewResourceDetector /
// WithAttributeFilter (added in v0.17.0). Apply the same attribute filter on
// the detected resource instead.
func newAzureVMDetector(filter attribute.Filter) resource.Detector {
	return &filteredDetector{Detector: azurevm.New(), filter: filter}
}

type filteredDetector struct {
	resource.Detector
	filter attribute.Filter
}

func (f *filteredDetector) Detect(ctx context.Context) (*resource.Resource, error) {
	res, err := f.Detector.Detect(ctx)
	if res == nil {
		return res, err
	}
	kept := make([]attribute.KeyValue, 0, res.Len())
	for _, kv := range res.Attributes() {
		if f.filter(kv) {
			kept = append(kept, kv)
		}
	}
	return resource.NewWithAttributes(res.SchemaURL(), kept...), err
}
