// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package procname // import "go.opentelemetry.io/obi/pkg/internal/procname"

import "errors"

func readInRoot(_, _ string) ([]byte, error) {
	return nil, errors.New("not supported on this platform")
}
