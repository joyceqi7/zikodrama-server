package mysqlstore

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"drama-server/internal/biz/operations"
)

// TestNewTikTokPublicationModelNormalizesZeroSchedulerTimes 防止暂停或终态任务把 Go 零时间写成 MySQL 非法日期。
func TestNewTikTokPublicationModelNormalizesZeroSchedulerTimes(t *testing.T) {
	model, err := newTikTokPublicationModel(operations.TikTokPublication{SubjectID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Unix(0, 0).UTC()
	if !model.NextAttemptAt.Equal(want) {
		t.Fatalf("next_attempt_at = %v, want %v", model.NextAttemptAt, want)
	}
	if !model.LeaseExpiresAt.Equal(want) {
		t.Fatalf("lease_expires_at = %v, want %v", model.LeaseExpiresAt, want)
	}
}

// TestPublicationTranscodeSeedPersistsWithoutPublicJSON 验证转码键前缀可恢复且不会进入管理端响应。
func TestPublicationTranscodeSeedPersistsWithoutPublicJSON(t *testing.T) {
	publication := operations.TikTokPublication{SubjectID: "1", TranscodeKeySeed: "transcode_test-seed", SkipTranscode: true}
	model, err := newTikTokPublicationModel(publication)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := model.domain()
	if err != nil || restored.TranscodeKeySeed != publication.TranscodeKeySeed || !restored.SkipTranscode {
		t.Fatalf("转码键前缀没有从数据库快照恢复：%+v %v", restored, err)
	}
	encoded, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(publication.TranscodeKeySeed)) || bytes.Contains(encoded, []byte("skipTranscode")) {
		t.Fatal("管理端响应泄漏转码键前缀")
	}
}

// TestLegacyTranscodeChoiceDefaultsEnabled 验证旧工作流没有跳过标记时，恢复后仍沿用原转码流程。
func TestLegacyTranscodeChoiceDefaultsEnabled(t *testing.T) {
	model, err := newTikTokPublicationModel(operations.TikTokPublication{SubjectID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := model.domain()
	if err != nil || restored.SkipTranscode {
		t.Fatal("改变旧任务转码策略", err)
	}
}
