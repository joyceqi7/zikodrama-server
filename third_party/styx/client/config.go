package client

import (
	"fmt"
	"net/url"
	"strings"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

func validateHTTPConfig(cfg *configv1.HTTPClientConfig) error {
	if cfg.GetSubsetSize() < 0 {
		return fmt.Errorf("client: HTTP subset_size must not be negative")
	}
	if endpoint := strings.TrimSpace(cfg.GetEndpoint()); cfg.GetEndpoint() != "" && endpoint == "" {
		return fmt.Errorf("client: HTTP endpoint must not be blank")
	} else if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("client: HTTP endpoint must be an HTTP(S) URL without embedded credentials")
		}
	}
	return validateMiddlewareConfig(cfg.GetMiddleware())
}

func validateGRPCConfig(cfg *configv1.GRPCClientConfig) error {
	if cfg.GetSubsetSize() < 0 || cfg.GetMaxRecvMsgSize() < 0 || cfg.GetMaxSendMsgSize() < 0 {
		return fmt.Errorf("client: gRPC size limits must not be negative")
	}
	if cfg.GetEndpoint() != "" && strings.TrimSpace(cfg.GetEndpoint()) == "" {
		return fmt.Errorf("client: gRPC endpoint must not be blank")
	}
	return validateMiddlewareConfig(cfg.GetMiddleware())
}

func validateMiddlewareConfig(cfg *configv1.ClientMiddlewareConfig) error {
	breaker := cfg.GetCircuitbreaker()
	if breaker == nil {
		return nil
	}
	if threshold := breaker.GetSuccessThreshold(); threshold < 0 || threshold > 1 {
		return fmt.Errorf("client: circuit breaker success_threshold must be between 0 and 1")
	}
	if breaker.GetRequestThreshold() < 0 || breaker.GetBucket() < 0 {
		return fmt.Errorf("client: circuit breaker limits must not be negative")
	}
	if breaker.GetWindow() != nil && breaker.GetWindow().AsDuration() <= 0 {
		return fmt.Errorf("client: circuit breaker window must be positive")
	}
	return nil
}
