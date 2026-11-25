package monetization

import (
	"context"
	"time"
)

// EntryAdRepository 只读取奖励账本中的入剧完整观看，不依赖展示事实的持久化。
type EntryAdRepository interface {
	// CountEntryAdCompletions 读取规则在 [开始,结束) 内的已结算次数；dramaID 为空时汇总该用户所有剧目。
	CountEntryAdCompletions(context.Context, string, string, string, time.Time, time.Time) (int, error)
}
