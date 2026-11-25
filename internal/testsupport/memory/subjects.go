package memory

import (
	"context"
	"drama-server/internal/biz/subject"
	"fmt"
	"sort"
	"time"
)

// ListSubjects 返回测试内存中的主体目录，不创建生产默认值。
func (s *Store) ListSubjects(context.Context) ([]subject.Subject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []subject.Subject{}
	for _, row := range s.subjects {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

// FindSubject 按测试主键读取主体。
func (s *Store) FindSubject(_ context.Context, id string) (subject.Subject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.subjects[id]
	if !ok {
		return row, subject.ErrNotFound
	}
	return row, nil
}

// CreateSubject 为测试主体分配独立主键并检查唯一身份。
func (s *Store) CreateSubject(_ context.Context, row *subject.Subject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subjects == nil {
		s.subjects = map[string]subject.Subject{}
	}
	for _, old := range s.subjects {
		if old.Name == row.Name || (row.ClientKey != "" && old.ClientKey == row.ClientKey) || (row.TikTokAppID != "" && old.TikTokAppID == row.TikTokAppID) {
			return subject.ErrConflict
		}
	}
	row.ID = s.allocateIDLocked()
	row.OwnerID = "subject-" + row.ID
	row.CreatedAt = time.Now().UTC()
	row.UpdatedAt = row.CreatedAt
	s.subjects[row.ID] = *row
	return nil
}

// UpdateSubject 模拟事务中的身份冻结和空密钥保留。
func (s *Store) UpdateSubject(_ context.Context, row subject.Subject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.subjects[row.ID]
	if !ok {
		return subject.ErrNotFound
	}
	for id, other := range s.subjects {
		if id != row.ID && (other.Name == row.Name || row.ClientKey != "" && other.ClientKey == row.ClientKey || row.TikTokAppID != "" && other.TikTokAppID == row.TikTokAppID) {
			return subject.ErrConflict
		}
	}
	if old.ClientKey != row.ClientKey || old.TikTokAppID != row.TikTokAppID {
		if row.ClientKey != "" && row.ClientSecret == "" {
			return subject.ErrNotConfigured
		}
		for _, p := range s.tiktokPublications {
			if fmt.Sprint(p.SubjectID) == row.ID {
				return subject.ErrConflict
			}
		}
	}
	row.OwnerID = old.OwnerID
	row.CreatedAt = old.CreatedAt
	if row.ClientSecret == "" && row.ClientKey != "" {
		row.ClientSecret = old.ClientSecret
	}
	row.UpdatedAt = time.Now().UTC()
	s.subjects[row.ID] = row
	return nil
}
