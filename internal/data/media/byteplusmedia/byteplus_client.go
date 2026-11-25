package byteplusmedia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod"
)

const maxBytePlusResponseSize = 4 << 20

// bytePlusSDKClient 为官方 SDK 补充调用方 context，并提供不落本地临时文件的素材流上传。
type bytePlusSDKClient struct {
	*vod.Vod
}

// CtxQuery 使用官方签名器发起可取消的 GET 请求。
func (client *bytePlusSDKClient) CtxQuery(ctx context.Context, api string, query url.Values) ([]byte, int, error) {
	return client.do(ctx, api, query, nil)
}

// CtxPost 使用官方签名器发起可取消的表单 POST 请求。
func (client *bytePlusSDKClient) CtxPost(ctx context.Context, api string, query, form url.Values) ([]byte, int, error) {
	return client.do(ctx, api, query, form)
}

func (client *bytePlusSDKClient) do(ctx context.Context, api string, query, form url.Values) ([]byte, int, error) {
	if client == nil || client.Vod == nil || client.Client == nil || client.ServiceInfo == nil {
		return nil, http.StatusServiceUnavailable, operationsInvalidRequest()
	}
	apiInfo := client.ApiInfoList[api]
	if apiInfo == nil {
		return nil, http.StatusInternalServerError, errors.New("byteplus api is not configured")
	}
	mergedQuery := mergeURLValues(apiInfo.Query, query)
	var body io.Reader
	if form != nil {
		body = strings.NewReader(mergeURLValues(apiInfo.Form, form).Encode())
	}
	contentType := ""
	if form != nil {
		contentType = "application/x-www-form-urlencoded"
	}
	return client.send(ctx, api, apiInfo.Method, apiInfo.Path, mergedQuery, body, contentType, apiInfo.Timeout)
}

// postEdit 使用官方签名器调用新版 JSON 剪辑接口，保留 Worker 取消与请求超时。
func (client *bytePlusSDKClient) postEdit(ctx context.Context, action string, body []byte) ([]byte, int, error) {
	return client.send(ctx, action, http.MethodPost, "/", url.Values{
		"Action": {action}, "Version": {"2025-01-01"},
	}, bytes.NewReader(body), "application/json", 0)
}

func (client *bytePlusSDKClient) send(ctx context.Context, api, method, path string, query url.Values, body io.Reader, contentType string, timeout time.Duration) ([]byte, int, error) {
	endpoint := url.URL{
		Scheme: client.ServiceInfo.Scheme, Host: client.ServiceInfo.Host,
		Path: path, RawQuery: query.Encode(),
	}
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), endpoint.String(), body)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.New("build byteplus request")
	}
	request.Header = client.ServiceInfo.Header.Clone()
	if contentType != "" {
		request.Header.Set("content-type", contentType)
	}
	request = client.ServiceInfo.Credentials.Sign(request)
	if timeout <= 0 {
		timeout = client.ServiceInfo.Timeout
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	response, err := client.Vod.Client.Client.Do(request.WithContext(requestCtx))
	if err != nil {
		return nil, http.StatusServiceUnavailable, errors.New("call byteplus api")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBytePlusResponseSize))
	if err != nil {
		return nil, response.StatusCode, errors.New("read byteplus response")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return raw, response.StatusCode, fmt.Errorf("byteplus api %s returned HTTP %d", api, response.StatusCode)
	}
	return raw, response.StatusCode, nil
}

func mergeURLValues(sources ...url.Values) url.Values {
	result := url.Values{}
	for _, source := range sources {
		for key, values := range source {
			result[key] = append([]string(nil), values...)
		}
	}
	return result
}

func operationsInvalidRequest() error {
	return errors.New("invalid byteplus request")
}
