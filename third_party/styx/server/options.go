package server

import (
	"fmt"
	"net"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
)

// HTTPOption 只开放不会替换 Styx 治理链的 HTTP 扩展 seam。
type HTTPOption func(*httpOptions) error

type httpOptions struct {
	listener net.Listener
	filters  []kratoshttp.FilterFunc
}

// WithHTTPListener 使用调用方预绑定的 Listener，不改变中间件或 Filter 治理。
func WithHTTPListener(listener net.Listener) HTTPOption {
	return func(options *httpOptions) error {
		if listener == nil {
			return fmt.Errorf("server: HTTP listener is required")
		}
		if options.listener != nil {
			return fmt.Errorf("server: HTTP listener can only be configured once")
		}
		options.listener = listener
		return nil
	}
}

// WithHTTPFilter 在 Styx Recovery 内追加应用 Filter，不能替换治理链。
func WithHTTPFilter(filter kratoshttp.FilterFunc) HTTPOption {
	return func(options *httpOptions) error {
		if filter == nil {
			return fmt.Errorf("server: HTTP filter is required")
		}
		options.filters = append(options.filters, filter)
		return nil
	}
}

func resolveHTTPOptions(optionList ...HTTPOption) (httpOptions, error) {
	var resolved httpOptions
	for _, option := range optionList {
		if option == nil {
			return httpOptions{}, fmt.Errorf("server: HTTP option is required")
		}
		if err := option(&resolved); err != nil {
			return httpOptions{}, err
		}
	}
	return resolved, nil
}
