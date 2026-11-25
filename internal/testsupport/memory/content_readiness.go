package memory

import (
	"context"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/operations"
)

// PrepareDrama 确认单条内容就绪，不生成版本或改变渠道任务。
func (s *Store) PrepareDrama(ctx context.Context, id, actor string) (*consumer.Drama, error) {
	if err := s.PrepareDramas(ctx, []string{id}, actor); err != nil {
		return nil, err
	}
	return s.FindDrama(ctx, id)
}

// PrepareDramas 先校验全部内容，再在同一临界区写入就绪状态；失败时整批不变。
func (s *Store) PrepareDramas(_ context.Context, ids []string, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		d, ok := s.dramas[id]
		if !ok {
			return consumer.ErrNotFound
		}
		if err := operations.CheckContentReady(d); err != nil {
			return err
		}
	}
	for _, id := range ids {
		d := s.dramas[id]
		if d.PublishStatus == "ready" {
			continue
		}
		d.PublishStatus = "ready"
		s.dramas[id] = cloneDrama(d)
		if actor != "" {
			s.dramaOperators[id] = actor
		}
	}
	return nil
}
