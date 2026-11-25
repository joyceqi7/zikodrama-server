package otel

import (
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/sdk/trace"
)

// NewSampler creates a trace Sampler based on the sampler name and rate.
//
// Supported samplers:
//   - always_on: Always sample (for debugging/low-traffic)
//   - always_off: Never sample (disable tracing)
//   - traceidratio: Sample based on TraceID ratio
//   - parentbased_always_on: Respect parent, default to always sample
//   - parentbased_always_off: Respect parent, default to never sample
//   - parentbased_traceidratio: Respect parent, default to ratio (default)
//
// The rate parameter is only used for ratio-based samplers (0.0-1.0).
// Default sampler is parentbased_traceidratio with rate 1.0.
func NewSampler(name string, rate float64) (trace.Sampler, error) {
	if rate < 0 || rate > 1 {
		return nil, fmt.Errorf("otel: sample rate must be between 0 and 1")
	}
	switch strings.ToLower(name) {
	case "":
		return trace.ParentBased(trace.AlwaysSample()), nil
	case "always_on":
		return trace.AlwaysSample(), nil
	case "always_off":
		return trace.NeverSample(), nil
	case "traceidratio":
		return trace.TraceIDRatioBased(rate), nil
	case "parentbased_always_on":
		return trace.ParentBased(trace.AlwaysSample()), nil
	case "parentbased_always_off":
		return trace.ParentBased(trace.NeverSample()), nil
	case "parentbased_traceidratio":
		return trace.ParentBased(trace.TraceIDRatioBased(rate)), nil
	default:
		return nil, fmt.Errorf("otel: unsupported sampler %q", name)
	}
}
