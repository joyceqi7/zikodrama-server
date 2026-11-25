package byteplusmedia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod"
)

type uploadRoundTrip func(*http.Request) (*http.Response, error)

func (fn uploadRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestVODObjectRequestStopsImmediatelyOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	client := &bytePlusSDKClient{Vod: vod.NewInstance()}
	client.Vod.Client.Client.Transport = uploadRoundTrip(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	completed := make(chan error, 1)
	go func() {
		completed <- client.putVODObject(ctx, bytes.NewReader([]byte("video")), 5, "upload.example", "object", "test-auth")
	}()
	<-started
	cancel()
	if err := <-completed; !errors.Is(err, context.Canceled) {
		t.Fatalf("对象传输必须随任务取消: %v", err)
	}
}

func TestVODMultipartUsesOfficialPartAndMergeShape(t *testing.T) {
	client := &bytePlusSDKClient{Vod: vod.NewInstance()}
	steps := []string{}
	client.Vod.Client.Client.Transport = uploadRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "test-auth" || request.Header.Get("X-Storage-Mode") != "gateway" {
			t.Fatal("分片请求缺少官方对象网关授权或模式")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var response string
		switch {
		case request.URL.RawQuery == "uploads":
			steps = append(steps, "init")
			response = `{"success":0,"payload":{"uploadID":"upload-1"}}`
		case request.URL.Query().Get("partNumber") == "1":
			steps = append(steps, "part-1")
			if len(body) != vodPartSize || request.Header.Get("Content-CRC32") == "" {
				t.Fatal("首片大小或 CRC32 错误")
			}
			response = `{"success":0}`
		case request.URL.Query().Get("partNumber") == "2":
			steps = append(steps, "part-2")
			if len(body) != 1 {
				t.Fatal("末片大小错误")
			}
			response = `{"success":0}`
		default:
			steps = append(steps, "merge")
			if request.URL.Query().Get("uploadID") != "upload-1" || !strings.HasPrefix(string(body), "0:") || !strings.Contains(string(body), ",1:") {
				t.Fatal("合并体必须沿用官方编号格式")
			}
			response = `{"success":0}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})
	data := make([]byte, vodPartSize+1)
	if err := client.putVODObject(context.Background(), bytes.NewReader(data), int64(len(data)), "upload.example", "object", "test-auth"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(steps, ","); got != "init,part-1,part-2,merge" {
		t.Fatal(got)
	}
}

func TestVODObjectHTTPFailurePreservesStageAndSafeDiagnostics(t *testing.T) {
	data := make([]byte, vodPartSize+1)
	for _, testCase := range []struct {
		name  string
		stage string
		size  int64
	}{
		{name: "single", stage: "single object upload", size: 1},
		{name: "init", stage: "multipart init", size: int64(len(data))},
		{name: "part", stage: "multipart part", size: int64(len(data))},
		{name: "merge", stage: "multipart merge", size: int64(len(data))},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := &bytePlusSDKClient{Vod: vod.NewInstance()}
			client.Vod.Client.Client.Transport = uploadRoundTrip(func(request *http.Request) (*http.Response, error) {
				stage := "single"
				switch {
				case request.URL.RawQuery == "uploads":
					stage = "init"
				case request.URL.Query().Has("partNumber"):
					stage = "part"
				case request.URL.Query().Has("uploadID"):
					stage = "merge"
				}
				if stage == testCase.name {
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Header:     http.Header{"X-Tt-Logid": {"20260929ABC"}},
						Body: io.NopCloser(strings.NewReader(
							`{"error":{"error_code":4004,"message":"token=private https://media.example/video"},"auth":"secret"}`)),
					}, nil
				}
				response := `{"success":0}`
				if stage == "init" {
					response = `{"success":0,"payload":{"uploadID":"upload-1"}}`
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
			})
			reader := bytes.NewReader(data[:testCase.size])
			err := client.putVODObject(context.Background(), reader, testCase.size, "upload.example", "private-object", "private-auth")
			if err == nil || !strings.Contains(err.Error(), testCase.stage) ||
				!strings.Contains(err.Error(), "HTTP 400 provider_code=4004 request_id=20260929ABC") {
				t.Fatalf("对象上传错误应包含阶段和安全诊断: %v", err)
			}
			for _, sensitive := range []string{"private", "secret", "media.example", "upload.example"} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("对象上传错误泄漏敏感数据: %v", err)
				}
			}
		})
	}
}

func TestVODObjectHTTPFailureRejectsUnsafeRequestID(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"X-Tt-Logid": {"token=private https://media.example/video"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"secret"}}`)),
	}
	err := vodObjectHTTPError(response)
	if err.Error() != "byteplus object transfer returned HTTP 400" {
		t.Fatalf("不可信的响应字段不得进入错误链: %v", err)
	}
}

func TestUploadStreamAcceptsAdditionalBytePlusResponseFields(t *testing.T) {
	client := &bytePlusSDKClient{Vod: vod.NewInstance()}
	client.Vod.Client.SetAccessKey("test-ak")
	client.Vod.Client.SetSecretKey("test-sk")
	steps := []string{}
	client.Vod.Client.Client.Transport = uploadRoundTrip(func(request *http.Request) (*http.Response, error) {
		var payload string
		switch request.URL.Query().Get("Action") {
		case "ApplyUploadInfo":
			steps = append(steps, "apply")
			payload = `{"ResponseMetadata":{},"Result":{"Data":{"UploadAddress":{"StoreInfos":[{"StoreUri":"object","Auth":"test-auth"}],"UploadHosts":["upload.example"],"SessionKey":"session-1","Cloud":"new-provider-field"}}}}`
		case "CommitUploadInfo":
			steps = append(steps, "commit")
			if request.URL.Query().Get("SessionKey") != "session-1" {
				return nil, errors.New("提交上传时没有沿用申请阶段的 SessionKey")
			}
			payload = `{"ResponseMetadata":{},"Result":{"Data":{"Mid":"material-1","Cloud":"new-provider-field"}}}`
		case "":
			steps = append(steps, "object")
			if request.Method != http.MethodPut || request.URL.Host != "upload.example" || request.Header.Get("Authorization") != "test-auth" {
				return nil, errors.New("对象上传请求信息不正确")
			}
			payload = `{"success":0}`
		default:
			return nil, errors.New("收到非预期的 BytePlus 请求")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
	})

	result, status, err := client.UploadStreamWithCallback(&materialStreamUploadRequest{
		Context: context.Background(), SpaceName: "test", FileType: "image", Content: bytes.NewReader([]byte("image")), Size: 5,
	})
	if err != nil || status != http.StatusOK || result.GetResult().GetData().GetMid() != "material-1" {
		t.Fatalf("含扩展字段的 BytePlus 响应应完成上传: status=%d result=%v err=%v", status, result, err)
	}
	if got := strings.Join(steps, ","); got != "apply,object,commit" {
		t.Fatalf("上传步骤不正确: %s", got)
	}
}

func TestUploadStreamIdentifiesInterruptedObjectTransfer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &bytePlusSDKClient{Vod: vod.NewInstance()}
	client.Vod.Client.SetAccessKey("test-ak")
	client.Vod.Client.SetSecretKey("test-sk")
	client.Vod.Client.Client.Transport = uploadRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("Action") == "ApplyUploadInfo" {
			payload := `{"ResponseMetadata":{},"Result":{"Data":{"UploadAddress":{"StoreInfos":[{"StoreUri":"object","Auth":"test-auth"}],"UploadHosts":["upload.example"],"SessionKey":"session-1"}}}}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
		}
		if request.Method != http.MethodPut {
			return nil, errors.New("对象上传失败后不应进入提交阶段")
		}
		cancel()
		return nil, request.Context().Err()
	})

	_, _, err := client.UploadStreamWithCallback(&materialStreamUploadRequest{
		Context: ctx, SpaceName: "test", FileType: "image", Content: bytes.NewReader([]byte("image")), Size: 5,
	})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "upload byteplus object") {
		t.Fatalf("对象上传中断应保留原因和阶段: %v", err)
	}
}
