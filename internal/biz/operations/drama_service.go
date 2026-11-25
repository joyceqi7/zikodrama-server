package operations

// DramaService 是短剧聚合的用例边界；媒资、发布和授权都属于剧的生命周期。
type DramaService struct {
	// repositories 只保存短剧生命周期需要的持久化端口。
	repositories dramaRepositories
	// media 提供 VOD 官方能力。
	media MediaProvider
	// mediaJobs 持有媒资 Worker 的进程生命周期。
	mediaJobs *mediaJobRunner
	// tiktokProviderFactory 按平台应用创建官方客户端。
	tiktokProviderFactory TikTokMediaProviderFactory
	// publicationJobs 持有发布 Worker 的进程生命周期。
	publicationJobs *publicationJobRunner
}

// dramaRepositories 限制短剧模块只能访问内容、剧的媒资任务和应用身份。
type dramaRepositories struct {
	// Content 维护剧目、分发与发布事实。
	Content ContentPorts
	// Media 保存剧的上传、处理和远端清理任务。
	Media MediaPorts
	// Application 提供拥有方及被授权应用的身份查询。
	Application ApplicationPorts
}

// NewDramaService 组合短剧所需端口；不接收投放、报表或变现配置依赖。
func NewDramaService(content ContentPorts, mediaJobs MediaPorts, applications ApplicationRepository, media MediaProvider) *DramaService {
	return &DramaService{repositories: dramaRepositories{Content: content, Media: mediaJobs, Application: ApplicationPorts{Registry: applications}}, media: media}
}
