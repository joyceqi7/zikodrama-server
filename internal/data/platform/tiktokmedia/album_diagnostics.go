package tiktokmedia

import (
	"strconv"
	"strings"

	"drama-server/internal/biz/operations"
)

// readAlbumPayload 保留更新和查询实际解码的层级，不改变响应字段选择或业务结果。
func (d *platformCallDiagnostic) readAlbumPayload(payload string) {
	if d.path == "/v2/sg/shortdrama/album/update/" || d.path == "/v2/sg/shortdrama/album/query/" {
		d.albumPayload = payload
	}
}

// episodeMappingKeyCounts 仅统计动态映射键的类别，任何键名、平台标识及映射值都不进入日志。
func episodeMappingKeyCounts(episodes []operations.AlbumEpisodeDraft, raw map[string]stringID) map[string]int {
	existing := make(map[string]bool, len(episodes))
	sequences := make(map[int]bool, len(episodes))
	for _, episode := range episodes {
		if episode.PlatformEpisodeID != "" {
			existing[episode.PlatformEpisodeID] = true
		}
		sequences[episode.Sequence] = true
	}
	counts := make(map[string]int)
	for key := range raw {
		kind := "unrecognized"
		switch {
		case existing[key]:
			kind = "existing_id"
		case strings.HasPrefix(key, "seq:"):
			sequence, err := strconv.Atoi(strings.TrimPrefix(key, "seq:"))
			kind = "invalid_sequence"
			if err == nil && sequence > 0 {
				kind = "unexpected_sequence"
				if sequences[sequence] {
					kind = "sequence"
				}
			}
		default:
			if _, err := numericID(key); err == nil {
				kind = "numeric"
			}
		}
		counts[kind]++
	}
	return counts
}
