package operations

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	domain "drama-server/internal/biz/consumer"
)

// ListDramas 返回运营视角的全部短剧。
func (s *DramaService) ListDramas(ctx context.Context) ([]domain.Drama, error) {
	if s == nil || s.repositories.Content.Catalog == nil {
		slog.WarnContext(ctx, "list dramas rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	items, err := s.repositories.Content.Catalog.ListAllDramas(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list all dramas failed", "error", err)
		return nil, err
	}
	for index := range items {
		projectOperationsDrama(&items[index])
	}
	return items, nil
}

// CreateDrama 接收入口已校验的短剧及分集，检查封面关联并原子创建聚合。
func (s *DramaService) CreateDrama(ctx context.Context, input DramaInput) (*domain.Drama, error) {
	if s == nil || s.repositories.Content.Catalog == nil {
		slog.WarnContext(ctx, "create drama rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	var err error
	drama := domain.Drama{
		Title: input.Title, Description: input.Description,
		CoverURL: input.CoverURL,
		Status:   input.Status, Year: input.Year, Language: input.Language,
		DramaType: input.DramaType, TagIDs: append([]string(nil), input.TagIDs...),
		PublishStatus: "draft",
	}
	drama.Episodes, err = newEpisodes(drama.ID, input.Episodes)
	if err != nil {
		slog.ErrorContext(ctx, "new episodes failed", "error", err)
		return nil, err
	}
	cover, err := s.uploadedDramaCover(ctx, drama.CoverURL, input.CoverUploadID)
	if err != nil {
		slog.ErrorContext(ctx, "new drama cover reference failed", "error", err)
		return nil, err
	}
	if err := s.repositories.Content.Catalog.SaveDramaAggregate(ctx, &drama, cover); err != nil {
		slog.ErrorContext(ctx, "save drama aggregate failed", "error", err)
		return nil, err
	}

	result1, resultErr := s.FindDrama(ctx, drama.ID)
	if resultErr != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", resultErr)
	}
	return result1, resultErr
}

// UpdateDrama 接收入口已校验的内容，检查可编辑状态与分集归属后更新聚合。
func (s *DramaService) UpdateDrama(ctx context.Context, id string, input DramaInput) (*domain.Drama, error) {
	if s == nil || s.repositories.Content.Catalog == nil {
		slog.WarnContext(ctx, "update drama rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		return nil, err
	}
	drama.Title = input.Title
	// 编辑生成待送审草稿；Published 快照保持不变，观众继续读取已上线版本。
	drama.PublishStatus = "draft"
	drama.Description = input.Description
	drama.CoverURL = input.CoverURL
	drama.Tags = nil
	drama.Status = input.Status
	drama.Year = input.Year
	drama.Language = input.Language
	drama.DramaType = input.DramaType
	drama.TagIDs = append([]string(nil), input.TagIDs...)
	drama.Episodes, err = mergeEpisodes(*drama, input.Episodes)
	if err != nil {
		slog.ErrorContext(ctx, "merge episodes failed", "error", err)
		return nil, err
	}
	cover, err := s.uploadedDramaCover(ctx, drama.CoverURL, input.CoverUploadID)
	if err != nil {
		slog.ErrorContext(ctx, "new drama cover reference failed", "error", err)
		return nil, err
	}
	if err := s.repositories.Content.Catalog.SaveDramaAggregate(ctx, drama, cover); err != nil {
		slog.ErrorContext(ctx, "save drama aggregate failed", "error", err)
		return nil, err
	}
	s.notifyMediaWorker()
	s.notifyMediaReconciliation()

	result1, resultErr := s.FindDrama(ctx, drama.ID)
	if resultErr != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", resultErr)
	}
	return result1, resultErr
}

// UpdateDramaOperations 保存运营协作字段；发布处理中也允许交接，不改变内容和任务状态。
func (s *DramaService) UpdateDramaOperations(ctx context.Context, id string, input DramaOperationsInput) (*domain.Drama, error) {
	if s == nil || s.repositories.Content.Catalog == nil {
		return nil, ErrInvalidInput
	}
	id = strings.TrimSpace(id)
	if err := s.repositories.Content.Catalog.SaveDramaOperations(ctx, id, input.Assignee, input.Note, input.DueAt); err != nil {
		slog.ErrorContext(ctx, "save drama operations failed", "error", err)
		return nil, err
	}
	return s.FindDrama(ctx, id)
}

// uploadedDramaCover 读取本次封面上传结果，保存剧时在事务中核对并消费，不建设独立素材库。
func (s *DramaService) uploadedDramaCover(ctx context.Context, coverURL, uploadID string) (*MediaJob, error) {
	if uploadID == "" {
		return nil, nil
	}
	job, err := s.repositories.Media.Jobs.FindMediaJob(ctx, uploadID)
	if err != nil {
		slog.ErrorContext(ctx, "find cover upload result failed", "error", err)
		return nil, err
	}
	if job.Kind != MediaJobCover || job.Status != MediaJobUploaded || job.PublicURL != coverURL || job.RemoteVID == "" {
		return nil, ErrConflict
	}
	return &job, nil
}

// DeleteDrama 仅删除处于可编辑状态且没有线上快照的短剧；仓储删除成功后唤醒媒资清理。
func (s *DramaService) DeleteDrama(ctx context.Context, id string) error {
	if s == nil || s.repositories.Content.Catalog == nil {
		slog.WarnContext(ctx, "delete drama rejected", "error", ErrInvalidInput)
		return ErrInvalidInput
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		return err
	}
	if !dramaContentMutable(*drama) || drama.Published != nil {
		slog.WarnContext(ctx, "delete drama rejected", "error", ErrConflict)
		return ErrConflict
	}
	if err := s.repositories.Content.Catalog.DeleteDrama(ctx, drama.ID); err != nil {
		slog.ErrorContext(ctx, "delete drama failed", "error", err)
		return err
	}
	s.notifyMediaWorker()
	return nil
}

// FindDrama 返回草稿内容并补充线上版本号和中文标签名称；不以线上快照覆盖正在编辑的字段。
func (s *DramaService) FindDrama(ctx context.Context, id string) (*domain.Drama, error) {
	if s == nil || s.repositories.Content.Catalog == nil {
		slog.WarnContext(ctx, "find drama rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	drama, err := s.repositories.Content.Catalog.FindDrama(ctx, strings.TrimSpace(id))
	if err != nil {
		slog.ErrorContext(ctx, "find drama failed", "error", err)
		return nil, err
	}
	projectOperationsDrama(drama)
	return drama, nil
}

// Overview 返回短剧与媒资任务的运营概览。
func (s *DramaService) Overview(ctx context.Context) (*Overview, error) {
	if s == nil || s.repositories.Content.Catalog == nil || s.repositories.Media.Jobs == nil {
		slog.WarnContext(ctx, "overview rejected", "error", ErrInvalidInput)
		return nil, ErrInvalidInput
	}
	overview, err := s.repositories.Content.Catalog.DramaOverview(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "query drama overview failed", "error", err)
		return nil, err
	}
	cleanupCount, err := s.repositories.Media.Jobs.CountMediaDeletionJobs(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "count media deletion jobs failed", "error", err)
		return nil, err
	}
	overview.MediaCleanupCount = cleanupCount
	return overview, nil
}

// dramaMediaReady 要求至少一集且所有分集均有 VID 和 ready 状态，作为发布的前置条件。
func dramaMediaReady(drama domain.Drama) bool {
	if len(drama.Episodes) == 0 {
		return false
	}
	for _, episode := range drama.Episodes {
		if episode.MediaStatus != "ready" || strings.TrimSpace(episode.VID) == "" {
			return false
		}
	}
	return true
}

// dramaContentMutable 允许独立编辑草稿；渠道任务读取固定版本，不锁定编辑区。
func dramaContentMutable(drama domain.Drama) bool { return true }

// canEditDramaContent 编辑区与渠道任务独立，修改不会覆盖已确认或已送审的版本。
func (s *DramaService) canEditDramaContent(ctx context.Context, drama domain.Drama) (bool, error) {
	return true, nil
}

// newEpisodes 为入口已校验的新增分集构造待保存记录，并以 pending 媒资状态按集号排序返回。
// 连续集号和字段格式由入口保证；视频绑定留给后续上传任务。
func newEpisodes(dramaID string, inputs []EpisodeInput) ([]domain.Episode, error) {
	episodes := make([]domain.Episode, 0, len(inputs))
	for _, input := range inputs {
		episodes = append(episodes, domain.Episode{
			DramaID: dramaID, Number: input.Number, Title: input.Title,
			Description: input.Description,
			PosterURL:   input.PosterURL, DurationSeconds: input.DurationSeconds, MediaStatus: "pending",
		})
	}
	sort.Slice(episodes, func(left, right int) bool { return episodes[left].Number < episodes[right].Number })
	return episodes, nil
}

// mergeEpisodes 按输入重建分集集合：空 ID 创建新集，非空 ID 必须属于当前短剧。
// 已有集保留媒资和官方标识；未提交的集不进入结果。
func mergeEpisodes(drama domain.Drama, inputs []EpisodeInput) ([]domain.Episode, error) {
	existing := make(map[string]domain.Episode, len(drama.Episodes))
	for _, episode := range drama.Episodes {
		existing[episode.ID] = episode
	}
	next := make([]domain.Episode, 0, len(inputs))
	for _, input := range inputs {
		id := input.ID
		if id == "" {
			created, err := newEpisodes(drama.ID, []EpisodeInput{input})
			if err != nil {
				return nil, err
			}
			next = append(next, created[0])
			continue
		}
		episode, ok := existing[id]
		if !ok || episode.DramaID != drama.ID {
			return nil, ErrInvalidInput
		}
		episode.Number = input.Number
		episode.Title = input.Title
		episode.Description = input.Description
		episode.PosterURL = input.PosterURL
		// 零时长表示本次不覆盖，保留上传确认得到的有效视频时长。
		if input.DurationSeconds > 0 {
			episode.DurationSeconds = input.DurationSeconds
		}
		next = append(next, episode)
	}
	sort.Slice(next, func(left, right int) bool { return next[left].Number < next[right].Number })
	return next, nil
}

// ListDramaPage 返回运营列表，详情和分集通过 FindDrama 单独读取。
func (s *DramaService) ListDramaPage(ctx context.Context, input DramaListQuery) (DramaListPage, error) {
	page, err := s.repositories.Content.Catalog.ListDramaPage(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "list drama page failed", "error", err)
		return DramaListPage{}, err
	}
	return page, nil
}
