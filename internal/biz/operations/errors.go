package operations

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

var (
	// ErrInvalidInput 表示入口输入或内部调用所需前置数据不合法。
	ErrInvalidInput = errors.New("invalid operations input")
	// ErrMediaNotConfigured 表示当前用例所需媒资能力或持久化端口未配置。
	ErrMediaNotConfigured = errors.New("media provider is not configured")
	// ErrMediaProcessing 表示远端处理尚未结束，调用方可以安排后续轮询。
	ErrMediaProcessing = errors.New("media is still processing")
	// ErrMediaProcessingTimeout 表示处理时间已超过任务创建时固化的截止策略。
	ErrMediaProcessingTimeout = errors.New("VOD media processing timed out")
	// ErrMediaNotReady 表示剧集视频尚未达到业务所需的就绪状态。
	ErrMediaNotReady = errors.New("episode media is not ready")
	// ErrConflict 表示资源状态、归属或并发版本与操作要求冲突。
	ErrConflict = errors.New("operation conflicts with current state")
	// ErrApplicationAdUnitsIncomplete 表示启用应用或策略所需的激励候选集合或单个插屏未配齐。
	ErrApplicationAdUnitsIncomplete = fmt.Errorf("%w: configure rewarded video ad units and one interstitial ad unit for each application", ErrConflict)
	// ErrMediaUploadDiscarded 表示新上传媒资未能绑定且已安排清理；同时可用 errors.Is 判断 ErrConflict。
	ErrMediaUploadDiscarded = fmt.Errorf("%w: uploaded media cleanup was scheduled", ErrConflict)
	// ErrPublicationNotReady 表示内容、视频等官方发布前置条件尚未满足。
	ErrPublicationNotReady = errors.New("TikTok publication prerequisites are not ready")
	// ErrPublicationUnavailable 表示当前缺少 TikTok 官方发布客户端能力。
	ErrPublicationUnavailable = errors.New("TikTok publication provider is not configured")
	// ErrCampaignLinkUnavailable 表示官方投放链接生成失败，不允许使用自行拼接链接替代。
	ErrCampaignLinkUnavailable = errors.New("official Minis link is unavailable")
)

// secureID 创建带业务前缀的不可预测标识。
func secureID(prefix string) (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(buffer), nil
}
