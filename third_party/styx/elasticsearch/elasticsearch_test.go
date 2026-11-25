package elasticsearch

import (
	"context"
	"testing"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

func TestElasticsearchConfigDoesNotExposeSearchBodyCapture(t *testing.T) {
	descriptor := (&configv1.ElasticsearchConfig{}).ProtoReflect().Descriptor()
	if field := descriptor.Fields().ByName("capture_search_body"); field != nil {
		t.Fatalf("Elasticsearch 配置不得允许把搜索请求体写入 Span：%s", field.FullName())
	}
}

func TestBuildESTLSConfigRequiresCertificatePair(t *testing.T) {
	if _, err := buildESTLSConfig(&configv1.ElasticsearchTLSConfig{KeyFile: "client-key.pem"}); err == nil {
		t.Fatal("Elasticsearch 客户端证书和私钥未成对配置时必须失败")
	}
}

func TestNewElasticsearchRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewElasticsearch(ctx, "audit", &configv1.ElasticsearchConfig{
		Addresses: []string{"http://127.0.0.1:9200"},
	}, newElasticsearchRuntime(t))
	if err == nil {
		t.Fatal("已取消的初始化 Context 必须失败")
	}
}

func TestNewElasticsearchRejectsInvalidAddressAndLimits(t *testing.T) {
	for _, config := range []*configv1.ElasticsearchConfig{
		{Addresses: []string{"http://user:secret@127.0.0.1:9200"}},
		{Addresses: []string{"http://127.0.0.1:9200"}, MaxRetries: -1},
		{Addresses: []string{"http://127.0.0.1:9200"}, RetryOnStatus: []int32{999}},
	} {
		if _, err := NewElasticsearch(context.Background(), "audit", config, newElasticsearchRuntime(t)); err == nil {
			t.Fatal("无效 Elasticsearch 配置必须在连接前失败")
		}
	}
}

func newElasticsearchRuntime(t *testing.T) *styxruntime.Runtime {
	t.Helper()
	runtime, err := styxruntime.New(styxruntime.Config{
		Identity:     styxruntime.Identity{Name: "elasticsearch-test", Version: "test"},
		Dependencies: observe.NoopDependencies(),
	})
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	return runtime
}
