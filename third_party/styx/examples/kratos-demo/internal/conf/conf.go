// Package conf 只组合 Styx 已生成的配置类型，不重复维护字段定义。
package conf

import configv1 "gitee.com/linpbin/styx/api/config/v1"

type Bootstrap struct {
	App    *configv1.AppInfo    `json:"app,omitempty" yaml:"app,omitempty"`
	Server *Server              `json:"server,omitempty" yaml:"server,omitempty"`
	Data   *configv1.DataConfig `json:"data,omitempty" yaml:"data,omitempty"`
	Otel   *configv1.OTelConfig `json:"otel,omitempty" yaml:"otel,omitempty"`
}

type Server struct {
	HTTP *configv1.HTTPServerConfig `json:"http,omitempty" yaml:"http,omitempty"`
	GRPC *configv1.GRPCServerConfig `json:"grpc,omitempty" yaml:"grpc,omitempty"`
}
