package byteplusmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/byteplus-sdk/byteplus-sdk-golang/service/vod/models/response"
	"google.golang.org/protobuf/encoding/protojson"
)

const vodPartSize = 20 << 20
const maxVODObjectErrorSize = 4 << 10

// UploadStreamWithCallback 沿用官方 Apply/对象上传/Commit 协议，并将同一 context 绑定每次 HTTP 请求。
// SDK v1.0.72 的 UploadMediaInnerV2 在对象 PUT 中创建无 context 的请求，无法可靠停止超时上传。
func (client *bytePlusSDKClient) UploadStreamWithCallback(input *materialStreamUploadRequest) (*response.VodCommitUploadInfoResponse, int, error) {
	if client == nil || client.Vod == nil || input == nil || input.Context == nil || input.Content == nil || input.Size <= 0 {
		return nil, http.StatusBadRequest, operationsInvalidRequest()
	}
	ctx := input.Context
	applyQuery := url.Values{"SpaceName": {input.SpaceName}, "FileType": {input.FileType}}
	if input.FileName != "" {
		applyQuery.Set("FileName", input.FileName)
	}
	if input.FileExtension != "" {
		applyQuery.Set("FileExtension", input.FileExtension)
	}
	raw, status, err := client.CtxQuery(ctx, "ApplyUploadInfo", applyQuery)
	if err != nil {
		return nil, status, fmt.Errorf("apply byteplus upload info: %w", err)
	}
	var applied response.VodApplyUploadInfoResponse
	// BytePlus 会在上传响应中追加 SDK 尚未定义的字段；保留已知字段供下方完整性校验。
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &applied); err != nil {
		return nil, status, fmt.Errorf("decode byteplus apply upload response: %w", err)
	}
	if metadata := applied.GetResponseMetadata(); metadata != nil && metadata.GetError() != nil && metadata.GetError().GetCode() != "0" && metadata.GetError().GetCode() != "" {
		return nil, status, fmt.Errorf("byteplus apply upload rejected: %s", metadata.GetError().GetCode())
	}
	address := applied.GetResult().GetData().GetUploadAddress()
	if address == nil || len(address.GetUploadHosts()) == 0 || len(address.GetStoreInfos()) == 0 || address.GetStoreInfos()[0] == nil || address.GetSessionKey() == "" {
		return nil, status, errors.New("byteplus apply upload returned incomplete address")
	}
	host := address.GetUploadHosts()[0]
	store := address.GetStoreInfos()[0]
	if host == "" || strings.ContainsAny(host, "/?#@") || store.GetStoreUri() == "" || store.GetAuth() == "" {
		return nil, status, errors.New("byteplus apply upload returned invalid object address")
	}
	if err := client.putVODObject(ctx, input.Content, input.Size, host, store.GetStoreUri(), store.GetAuth()); err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("upload byteplus object: %w", err)
	}
	commitQuery := url.Values{
		"SpaceName": {input.SpaceName}, "SessionKey": {address.GetSessionKey()},
		"CallbackArgs": {input.CallbackArgs}, "Functions": {input.Functions},
		"VodUploadSource": {input.UploadSource},
	}
	raw, status, err = client.CtxQuery(ctx, "CommitUploadInfo", commitQuery)
	if err != nil {
		return nil, status, fmt.Errorf("commit byteplus upload info: %w", err)
	}
	var committed response.VodCommitUploadInfoResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &committed); err != nil {
		return nil, status, fmt.Errorf("decode byteplus commit upload response: %w", err)
	}
	if metadata := committed.GetResponseMetadata(); metadata != nil && metadata.GetError() != nil && metadata.GetError().GetCode() != "0" && metadata.GetError().GetCode() != "" {
		return &committed, status, fmt.Errorf("byteplus commit upload rejected: %s", metadata.GetError().GetCode())
	}
	return &committed, status, nil
}

// putVODObject 使用官方对象上传的 20 MiB 分片、CRC32 和合并格式；每个请求继承任务 context。
func (client *bytePlusSDKClient) putVODObject(ctx context.Context, reader io.Reader, size int64, host, objectID, auth string) error {
	if size < vodPartSize {
		part, err := readVODPart(reader, size)
		if err != nil {
			return err
		}
		if err := client.sendVODObject(ctx, host, objectID, auth, "", part, true, false); err != nil {
			return fmt.Errorf("byteplus single object upload: %w", err)
		}
		return nil
	}
	var initResult struct {
		Success int `json:"success"`
		Payload struct {
			UploadID string `json:"uploadID"`
		} `json:"payload"`
	}
	if err := client.requestVODObject(ctx, host, objectID, auth, "uploads", nil, false, true, &initResult); err != nil {
		return fmt.Errorf("byteplus multipart init: %w", err)
	}
	if initResult.Payload.UploadID == "" {
		return errors.New("byteplus multipart init returned no upload ID")
	}
	checksums := []string{}
	for number, remaining := 1, size; remaining > 0; number++ {
		partSize := remaining
		if partSize > vodPartSize {
			partSize = vodPartSize
		}
		part, err := readVODPart(reader, partSize)
		if err != nil {
			return err
		}
		query := url.Values{"partNumber": {strconv.Itoa(number)}, "uploadID": {initResult.Payload.UploadID}}.Encode()
		if err := client.sendVODObject(ctx, host, objectID, auth, query, part, true, true); err != nil {
			return fmt.Errorf("byteplus multipart part: %w", err)
		}
		checksums = append(checksums, fmt.Sprintf("%d:%08x", number-1, crc32.ChecksumIEEE(part)))
		remaining -= partSize
	}
	if err := client.sendVODObject(ctx, host, objectID, auth, url.Values{"uploadID": {initResult.Payload.UploadID}}.Encode(), []byte(strings.Join(checksums, ",")), false, true); err != nil {
		return fmt.Errorf("byteplus multipart merge: %w", err)
	}
	return nil
}

func readVODPart(reader io.Reader, size int64) ([]byte, error) {
	part := make([]byte, int(size))
	if _, err := io.ReadFull(reader, part); err != nil {
		return nil, fmt.Errorf("read byteplus upload part: %w", err)
	}
	return part, nil
}

func (client *bytePlusSDKClient) sendVODObject(ctx context.Context, host, objectID, auth, query string, body []byte, checksum, multipart bool) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var result struct {
			Success int `json:"success"`
		}
		last = client.requestVODObject(ctx, host, objectID, auth, query, body, checksum, multipart, &result)
		if last == nil {
			return nil
		}
	}
	return last
}

// requestVODObject 不记录授权、对象路径或媒体 URL；传输错误保留阶段与状态码。
func (client *bytePlusSDKClient) requestVODObject(ctx context.Context, host, objectID, auth, query string, body []byte, checksum, multipart bool, output any) error {
	target := url.URL{Scheme: "https", Host: host, Path: "/" + strings.TrimPrefix(objectID, "/"), RawQuery: query}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, target.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build byteplus object request: %w", err)
	}
	request.Header.Set("Authorization", auth)
	if checksum {
		request.Header.Set("Content-CRC32", fmt.Sprintf("%08x", crc32.ChecksumIEEE(body)))
	}
	if multipart {
		request.Header.Set("X-Storage-Mode", "gateway")
	}
	response, err := client.Vod.Client.Client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("byteplus object transfer failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return vodObjectHTTPError(response)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBytePlusResponseSize))
	if err != nil {
		return fmt.Errorf("read byteplus object response: %w", err)
	}
	var envelope struct {
		Success int `json:"success"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode byteplus object response: %w", err)
	}
	if envelope.Success != 0 {
		return fmt.Errorf("byteplus object operation rejected with code %d", envelope.Success)
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode byteplus object payload: %w", err)
	}
	return nil
}

// vodObjectHTTPError 只提取数值错误码和受限请求 ID；不把响应正文、授权或对象地址写入错误链。
func vodObjectHTTPError(response *http.Response) error {
	message := fmt.Sprintf("byteplus object transfer returned HTTP %d", response.StatusCode)
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxVODObjectErrorSize))
	if err == nil {
		var payload struct {
			Error struct {
				ErrorCode int64 `json:"error_code"`
				Code      int64 `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			code := payload.Error.ErrorCode
			if code == 0 {
				code = payload.Error.Code
			}
			if code > 0 {
				message += fmt.Sprintf(" provider_code=%d", code)
			}
		}
	}
	if requestID := safeVODObjectRequestID(response.Header.Get("X-Tt-Logid")); requestID != "" {
		message += " request_id=" + requestID
	}
	return errors.New(message)
}

// safeVODObjectRequestID 仅接受 BytePlus 请求 ID 的短 ASCII 字符，避免响应头注入日志内容。
func safeVODObjectRequestID(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return ""
		}
	}
	return value
}
