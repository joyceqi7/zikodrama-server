package consumer_test

import (
	"net/http"

	styxapierror "gitee.com/linpbin/styx/apierror"
	styxi18n "gitee.com/linpbin/styx/i18n"

	application "drama-server/internal/biz"
	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/visitor"
	"drama-server/internal/pkg/apimessages"
	httpapi "drama-server/internal/service/consumer"
)

// newConsumerHandler 为接口测试显式装配与生产一致的消费者启动用例。
func newConsumerHandler(service *domain.Service, visitors *visitor.Service, config httpapi.Config) http.Handler {
	handler := httpapi.NewHandler(
		service,
		visitors,
		application.NewConsumerBootstrap(visitors, service),
		config,
	)
	messages, err := apimessages.NewMessageBundle()
	if err != nil {
		panic(err)
	}
	return styxi18n.HTTPFilter(messages)(styxapierror.HTTPFilter()(handler))
}
