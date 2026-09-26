// Package domain holds Agritelem's core types and validation rules. It has no
// dependencies outside the standard library.
package domain

import (
	"errors"
	"fmt"
)

// Code is a stable, machine-readable reason for rejecting input. It is
// published as the `type` of an ICAR icarResponseMessageResource.
type Code string

// Rejection codes. Their string values are part of the public API.
const (
	CodeInvalidAnimalID         Code = "INVALID_ANIMAL_ID"
	CodeUnsupportedAnimalID     Code = "UNSUPPORTED_ANIMAL_SCHEME"
	CodeUnsupportedMetric       Code = "UNSUPPORTED_METRIC"
	CodeUnsupportedDuration     Code = "UNSUPPORTED_DURATION"
	CodeUnitMismatch            Code = "UNIT_MISMATCH"
	CodeAggregationMismatch     Code = "AGGREGATION_MISMATCH"
	CodePhysiologicalOutlier    Code = "PHYSIOLOGICAL_OUTLIER"
	CodeNonUTCTimestamp         Code = "NON_UTC_TIMESTAMP"
	CodeFutureTimestamp         Code = "FUTURE_TIMESTAMP"
	CodeMisalignedWindow        Code = "MISALIGNED_WINDOW"
	CodeSourceIDRequired        Code = "SOURCE_ID_REQUIRED"
	CodeSourceMismatch          Code = "SOURCE_MISMATCH"
	CodeDuplicateInResource     Code = "DUPLICATE_STATISTIC"
	CodeMissingField            Code = "MISSING_FIELD"
	CodeInvalidValue            Code = "INVALID_VALUE"
	CodeTooManyStatistics       Code = "TOO_MANY_STATISTICS"
	CodeUnknownAnimal           Code = "UNKNOWN_ANIMAL"
	CodeDeviceNotAssigned       Code = "DEVICE_NOT_ASSIGNED"
	CodeConflictingResubmission Code = "CONFLICTING_RESUBMISSION"
)

// Problem is a validation failure attributable to client input.
type Problem struct {
	Code   Code   `json:"code"`
	Field  string `json:"field,omitempty"` // JSON pointer of the offending value, relative to the resource
	Detail string `json:"detail"`
}

func (p *Problem) Error() string {
	if p.Field == "" {
		return fmt.Sprintf("%s: %s", p.Code, p.Detail)
	}
	return fmt.Sprintf("%s at %s: %s", p.Code, p.Field, p.Detail)
}

func problemf(code Code, field, format string, args ...any) *Problem {
	return &Problem{Code: code, Field: field, Detail: fmt.Sprintf(format, args...)}
}

// AsProblem returns err as a *Problem, prefixing its field with base. Errors
// that are not problems become CodeInvalidValue; nil stays nil.
func AsProblem(err error, base string) *Problem {
	if err == nil {
		return nil
	}
	var p *Problem
	if !errors.As(err, &p) {
		return &Problem{Code: CodeInvalidValue, Field: base, Detail: err.Error()}
	}
	return &Problem{Code: p.Code, Field: base + p.Field, Detail: p.Detail}
}
