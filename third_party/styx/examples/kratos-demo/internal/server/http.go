package server

import (
	"github.com/go-kratos/kratos/v2/transport/http"

	styxruntime "gitee.com/linpbin/styx/runtime"
	styxserver "gitee.com/linpbin/styx/server"

	"gitee.com/linpbin/styx/examples/kratos-demo/internal/conf"
	"gitee.com/linpbin/styx/examples/kratos-demo/internal/service"
)

// NewHTTPServer creates an HTTP server with Styx preset middlewares.
//
// Styx automatically adds:
// - Recovery middleware (with OTel panic metrics)
// - Tracing middleware (OpenTelemetry)
// - Metadata middleware (context propagation)
// - Logging middleware (structured logging with trace_id)
// - Validate middleware (protobuf validation)
func NewHTTPServer(c *conf.Server, userSvc *service.UserService, runtime *styxruntime.Runtime) (*http.Server, error) {
	// Create HTTP server with Styx preset middlewares
	// "kratos-demo" is the service name for metrics labels
	srv, err := styxserver.NewHTTPServer(c.HTTP, runtime)
	if err != nil {
		return nil, err
	}

	// Register your protobuf services here:
	// userv1.RegisterUserServiceHTTPServer(srv, userSvc)
	_ = userSvc // Placeholder - register actual service

	return srv, nil
}
