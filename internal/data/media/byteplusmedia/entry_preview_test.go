package byteplusmedia

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"drama-server/internal/biz/operations"
)

type entryPreviewClientStub struct {
	*uploadClientStub
	action string
	body   []byte
	result string
}

func (s *entryPreviewClientStub) postEdit(_ context.Context, action string, body []byte) ([]byte, int, error) {
	s.action, s.body = action, body
	return []byte(s.result), 200, nil
}

// TestEntryPreviewVODContract 验证按毫秒剪辑，等待态不发布，且只有独立短片可发布。
func TestEntryPreviewVODContract(t *testing.T) {
	source := uploadedSourceFixture("source-vid", "space")
	metadata := source.Result.MediaInfoList[0].SourceInfo
	metadata.Width, metadata.Height = 720, 1280
	client := &entryPreviewClientStub{uploadClientStub: &uploadClientStub{mediaInfoResponse: source},
		result: `{"Result":{"ReqId":"edit-req"}}`}
	provider := &Provider{client: client, spaceName: "space"}
	id, err := provider.SubmitEntryPreview(context.Background(), "source-vid", 5)
	if err != nil || id != "edit-req" || client.action != "SubmitDirectEditTaskAsync" {
		t.Fatalf("提交剪辑失败: %s %s %v", id, client.action, err)
	}
	var request struct {
		Uploader  string
		EditParam struct {
			Track [][]struct {
				Source     string
				TargetTime []int
			}
		}
	}
	if err := json.Unmarshal(client.body, &request); err != nil {
		t.Fatal(err)
	}
	track := request.EditParam.Track[0][0]
	if request.Uploader != "space" || track.Source != "vid://source-vid" || len(track.TargetTime) != 2 ||
		track.TargetTime[0] != 0 || track.TargetTime[1] != 5000 {
		t.Fatalf("剪辑参数错误: %s", client.body)
	}
	client.result = `{"Result":[{"ReqId":"edit-req","Status":"start"}]}`
	if _, err := provider.EntryPreviewResult(context.Background(), id, "source-vid", 5); !errors.Is(err, operations.ErrMediaProcessing) {
		t.Fatalf("任务执行中应等待: %v", err)
	}
	client.result = `{"Result":[{"ReqId":"edit-req","Status":"success","OutputVid":"source-vid"}]}`
	if _, err := provider.EntryPreviewResult(context.Background(), id, "source-vid", 5); err == nil {
		t.Fatal("正片 VID 被当成短片")
	}
	client.result = `{"Result":[{"ReqId":"edit-req","Status":"success","OutputVid":"clip-vid"}]}`
	client.mediaInfoResponse = uploadedSourceFixture("clip-vid", "space")
	if _, err := provider.EntryPreviewResult(context.Background(), id, "source-vid", 5); err == nil || client.publishRequest != nil {
		t.Fatalf("过长片段不应发布: %v", err)
	}
	client.mediaInfoResponse.Result.MediaInfoList[0].SourceInfo.Duration = 5
	vid, err := provider.EntryPreviewResult(context.Background(), id, "source-vid", 5)
	if err != nil || vid != "clip-vid" || client.publishRequest == nil || client.publishRequest.Vid != vid {
		t.Fatalf("未发布已核验短片: %s %v", vid, err)
	}
}
