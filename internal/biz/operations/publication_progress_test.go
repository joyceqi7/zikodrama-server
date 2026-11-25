package operations

import (
	"testing"
	"time"
)

// TestTranscodeProgressSeparatesChecksAndCompletion 验证时间边界和成功集合，轮询不能伪造进度。
func TestTranscodeProgressSeparatesChecksAndCompletion(t *testing.T) {
	start := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	batch := PublicationTranscode{VIDs: []string{"a", "b"}}
	if !recordTranscodeProgress(&batch, []string{"a"}, start) {
		t.Fatal("合法部分成功被拒绝")
	}
	if !recordTranscodeProgress(&batch, []string{"a"}, start.Add(time.Minute)) || !batch.LastProgressAt.Equal(start) || !batch.LastCheckedAt.Equal(start.Add(time.Minute)) {
		t.Fatal("轮询错误刷新了进展时间")
	}
	for _, bad := range [][]string{{"a", "other"}, {"a", "a"}, {}} {
		if recordTranscodeProgress(&batch, bad, start.Add(2*time.Minute)) {
			t.Fatalf("错误集合被接受：%v", bad)
		}
	}
	if !recordTranscodeProgress(&batch, []string{"b", "a"}, start.Add(3*time.Minute)) || !batch.LastProgressAt.Equal(start.Add(3*time.Minute)) {
		t.Fatal("新增完成分集没有推进时间")
	}
}
