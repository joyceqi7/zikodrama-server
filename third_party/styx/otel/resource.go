// Package otel provides OpenTelemetry integration for Styx.
package otel

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

// Default values for resource attributes
const (
	defaultVersion = "unknown"
	defaultEnv     = "development"
)

// NewResource creates an OTel Resource from AppInfo.
// Uses OpenTelemetry semantic conventions for standard attributes.
//
// The following semantic convention attributes are set:
//   - service.name (required): The logical name of the service
//   - service.version: The version of the service
//   - deployment.environment: The environment where the service is running
//   - service.namespace: A namespace for the service
//   - service.instance.id: A unique identifier for the service instance
//
// Default values:
//   - version defaults to "unknown"
//   - env defaults to "development"
//   - instance_id defaults to the hostname
func NewResource(ctx context.Context, app *configv1.AppInfo) (*resource.Resource, error) {
	// Get service name (required)
	serviceName := app.GetName()
	if serviceName == "" {
		serviceName = "unknown-service"
	}

	// Get version with default
	version := app.GetVersion()
	if version == "" {
		version = defaultVersion
	}

	// Get environment with default
	env := app.GetEnv()
	if env == "" {
		env = defaultEnv
	}

	// Get instance ID with hostname as default
	instanceID := app.GetInstanceId()
	if instanceID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		instanceID = hostname
	}

	// Build resource attributes
	attrs := []resource.Option{
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version),
			semconv.DeploymentEnvironment(env),
			semconv.ServiceInstanceID(instanceID),
		),
	}

	// Add namespace if provided
	if namespace := app.GetNamespace(); namespace != "" {
		attrs = append(attrs, resource.WithAttributes(
			semconv.ServiceNamespace(namespace),
		))
	}

	// Create and merge with default resource
	return resource.New(
		ctx,
		append(attrs, resource.WithFromEnv(), resource.WithTelemetrySDK())...,
	)
}
