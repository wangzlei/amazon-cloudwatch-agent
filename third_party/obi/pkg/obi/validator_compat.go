// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package obi // import "go.opentelemetry.io/obi/pkg/obi"

import (
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// ADOT-OBI (patch 99-pin-to-cwagent): the CloudWatch Agent pins
// github.com/go-playground/validator/v10 to v10.20.0, which predates the
// built-in "oneofci" tag (v10.22.0) used by the otel_*_export SDK log level
// fields. Without it validator panics ("Undefined validation function
// 'oneofci'") on every config validation, so register an equivalent
// case-insensitive oneof.
const validationTagOneOfCI = "oneofci"

func validateOneOfCI(fl validator.FieldLevel) bool {
	field := fl.Field()
	if field.Kind() != reflect.String {
		return false
	}
	value := field.String()
	for _, option := range strings.Fields(fl.Param()) {
		if strings.EqualFold(option, value) {
			return true
		}
	}
	return false
}
