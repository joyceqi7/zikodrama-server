package apierror

import (
	"context"
	"encoding/json"
	"net/http"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
)

// Payload 是统一错误响应中的公开字段。
type Payload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Error Payload `json:"error"`
}

type responseState struct {
	definition   *Definition
	templateData any
}

type responseStateContextKey struct{}

type deferredResponseWriter struct {
	http.ResponseWriter
	state *responseState
}

func (writer *deferredResponseWriter) WriteHeader(status int) {
	if writer.state.definition == nil {
		writer.ResponseWriter.WriteHeader(status)
	}
}

func (writer *deferredResponseWriter) Write(body []byte) (int, error) {
	if writer.state.definition != nil {
		return len(body), nil
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *deferredResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// HTTPFilter 在统一响应出口编码业务上报的 API 错误。
// 应放在 i18n.HTTPFilter 内层，使编码时可以读取请求级 Localizer。
func HTTPFilter() kratoshttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			state := &responseState{}
			ctx := context.WithValue(request.Context(), responseStateContextKey{}, state)
			request = request.WithContext(ctx)
			next.ServeHTTP(&deferredResponseWriter{ResponseWriter: response, state: state}, request)
			if state.definition != nil {
				writeHTTP(ctx, response, *state.definition, state.templateData)
			}
		})
	}
}

// WriteHTTP 编码不带模板数据的统一 HTTP 错误响应。
func WriteHTTP(ctx context.Context, response http.ResponseWriter, definition Definition) {
	WriteHTTPWithData(ctx, response, definition, nil)
}

// WriteHTTPWithData 编码带本地化模板数据的统一 HTTP 错误响应。
func WriteHTTPWithData(
	ctx context.Context,
	response http.ResponseWriter,
	definition Definition,
	templateData any,
) {
	if ctx != nil {
		state, ok := ctx.Value(responseStateContextKey{}).(*responseState)
		if ok && state != nil {
			if state.definition == nil {
				copied := definition
				state.definition = &copied
				state.templateData = templateData
				// 让审计等内层响应记录器观察到失败状态；最外层延迟 Writer 会屏蔽实际写出。
				response.WriteHeader(definition.Status)
			}
			return
		}
	}
	writeHTTP(ctx, response, definition, templateData)
}

func writeHTTP(ctx context.Context, response http.ResponseWriter, definition Definition, templateData any) {
	response.Header().Set("content-type", "application/json")
	response.WriteHeader(definition.Status)
	_ = json.NewEncoder(response).Encode(envelope{Error: Payload{
		Code: definition.Code, Message: definition.Message(ctx, templateData),
	}})
}
