package nacos

import (
	"fmt"
	"math"
	"strings"
)

const (
	defaultGroup   = "DEFAULT_GROUP"
	defaultCluster = "DEFAULT"
	defaultWeight  = 100.0
	defaultKind    = "grpc"
)

type options struct {
	group   string
	cluster string
	weight  float64
	kind    string // grpc、grpcs、http 或 https
}

func newOptions(opts ...Option) (*options, error) {
	o := &options{
		group:   defaultGroup,
		cluster: defaultCluster,
		weight:  defaultWeight,
		kind:    defaultKind,
	}
	for _, opt := range opts {
		opt(o)
	}
	if strings.TrimSpace(o.group) == "" {
		return nil, fmt.Errorf("nacos: group is required")
	}
	o.group = strings.TrimSpace(o.group)
	if strings.TrimSpace(o.cluster) == "" {
		return nil, fmt.Errorf("nacos: cluster is required")
	}
	o.cluster = strings.TrimSpace(o.cluster)
	if o.weight <= 0 || math.IsNaN(o.weight) || math.IsInf(o.weight, 0) {
		return nil, fmt.Errorf("nacos: weight must be finite and positive")
	}
	o.kind = strings.ToLower(strings.TrimSpace(o.kind))
	if !validEndpointKind(o.kind) {
		return nil, fmt.Errorf("nacos: kind must be grpc, grpcs, http, or https")
	}
	return o, nil
}

// Option is a function that configures the Registry.
type Option func(*options)

// WithGroup sets the service group.
func WithGroup(group string) Option {
	return func(o *options) {
		o.group = group
	}
}

// WithCluster sets the cluster name.
func WithCluster(cluster string) Option {
	return func(o *options) {
		o.cluster = cluster
	}
}

// WithWeight sets the service weight.
func WithWeight(weight float64) Option {
	return func(o *options) {
		o.weight = weight
	}
}

// WithKind sets the service kind (grpc、grpcs、http 或 https)。
func WithKind(kind string) Option {
	return func(o *options) {
		o.kind = kind
	}
}

func validEndpointKind(kind string) bool {
	switch kind {
	case "grpc", "grpcs", "http", "https":
		return true
	default:
		return false
	}
}
