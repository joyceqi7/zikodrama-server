package otel

import (
	"fmt"
	"strings"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func validateConfig(cfg *configv1.OTelConfig) error {
	if cfg == nil {
		return nil
	}

	switch normalizedProtocol(cfg) {
	case "", "grpc", "http":
	default:
		return fmt.Errorf("otel: unsupported protocol %q", cfg.GetProtocol())
	}
	switch normalizedCompression(cfg) {
	case "", "none", "gzip":
	default:
		return fmt.Errorf("otel: unsupported compression %q", cfg.GetCompression())
	}

	if cfg.GetTimeout() != nil && cfg.GetTimeout().AsDuration() <= 0 {
		return fmt.Errorf("otel: timeout must be positive")
	}
	if metricConfig := cfg.GetMetric(); metricConfig != nil && metricConfig.GetEnabled() {
		if metricConfig.GetInterval() != nil && metricConfig.GetInterval().AsDuration() <= 0 {
			return fmt.Errorf("otel: metric interval must be positive")
		}
	}
	if traceConfig := cfg.GetTrace(); traceConfig != nil {
		if _, err := NewSampler(traceConfig.GetSampler(), traceConfig.GetSampleRate()); err != nil {
			return err
		}
	}
	if logConfig := cfg.GetLog(); logConfig != nil {
		switch strings.ToLower(strings.TrimSpace(logConfig.GetLevel())) {
		case "", "debug", "info", "warn", "warning", "error":
		default:
			return fmt.Errorf("otel: unsupported log level %q", logConfig.GetLevel())
		}
	}
	for key := range cfg.GetHeaders() {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("otel: exporter header name must not be empty")
		}
	}
	if retry := cfg.GetRetry(); retry != nil && !retry.GetEnabled() &&
		(retry.GetInitialInterval() != nil || retry.GetMaxInterval() != nil || retry.GetMaxElapsedTime() != nil) {
		return fmt.Errorf("otel: retry intervals require retry.enabled=true")
	} else if retry != nil && retry.GetEnabled() {
		if retry.GetInitialInterval() != nil && retry.GetInitialInterval().AsDuration() <= 0 {
			return fmt.Errorf("otel: retry initial interval must be positive")
		}
		if retry.GetMaxInterval() != nil && retry.GetMaxInterval().AsDuration() <= 0 {
			return fmt.Errorf("otel: retry max interval must be positive")
		}
		if retry.GetMaxElapsedTime() != nil && retry.GetMaxElapsedTime().AsDuration() <= 0 {
			return fmt.Errorf("otel: retry max elapsed time must be positive")
		}
		if retry.GetInitialInterval() != nil && retry.GetMaxInterval() != nil &&
			retry.GetInitialInterval().AsDuration() > retry.GetMaxInterval().AsDuration() {
			return fmt.Errorf("otel: retry initial interval must not exceed max interval")
		}
	}

	if exportersEnabled(cfg) && normalizedEndpoint(cfg) == "" {
		return fmt.Errorf("otel: endpoint is required when an exporter is enabled")
	}
	return nil
}

func normalizedProtocol(cfg *configv1.OTelConfig) string {
	return strings.ToLower(strings.TrimSpace(cfg.GetProtocol()))
}

func normalizedCompression(cfg *configv1.OTelConfig) string {
	return strings.ToLower(strings.TrimSpace(cfg.GetCompression()))
}

func normalizedEndpoint(cfg *configv1.OTelConfig) string {
	return strings.TrimSpace(cfg.GetEndpoint())
}

func exportersEnabled(cfg *configv1.OTelConfig) bool {
	// 没有 endpoint 的 Trace 只提供请求关联标识，不创建远程导出器。
	return cfg.GetTrace().GetEnabled() && normalizedEndpoint(cfg) != "" ||
		cfg.GetMetric().GetEnabled() || cfg.GetLog().GetEnabled()
}
