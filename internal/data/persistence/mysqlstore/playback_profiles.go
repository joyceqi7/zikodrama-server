package mysqlstore

import (
	"drama-server/internal/biz/application"
	"gorm.io/gorm"
)

// attachPlaybackProfiles 批量读取配置；缺省应用始终沿用官方合同。
func attachPlaybackProfiles(tx *gorm.DB, apps []application.Application) error {
	ids := make([]string, 0, len(apps))
	for i := range apps {
		apps[i].PlaybackProvider = "tiktok_minis"
		ids = append(ids, apps[i].ID)
	}
	if len(ids) == 0 {
		return nil
	}
	var rows []applicationPlaybackProfileModel
	if err := tx.Where("application_id IN ?", ids).Find(&rows).Error; err != nil {
		return err
	}
	byID := map[string]applicationPlaybackProfileModel{}
	for _, row := range rows {
		byID[row.ApplicationID] = row
	}
	for i := range apps {
		if row, ok := byID[apps[i].ID]; ok {
			apps[i].PlaybackProvider = row.Provider
			apps[i].PlaybackRevision = row.Revision
		}
	}
	return nil
}
