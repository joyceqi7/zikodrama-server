package operations_test

import (
	"context"

	"drama-server/internal/biz/operations"
	"drama-server/internal/testsupport/officialmedia"
)

// publicationProviderStub 让 HTTP 测试覆盖完整官方阶段，不发起外部写入。
type publicationProviderStub struct{ officialmedia.Capabilities }

func (*publicationProviderStub) CreateImage(context.Context, string) (string, error) {
	return "cover-platform-1", nil
}

func (*publicationProviderStub) CreateAlbum(context.Context) (operations.CreateAlbumResult, error) {
	return operations.CreateAlbumResult{AlbumID: "7637420375425239060", Version: 1}, nil
}

func (*publicationProviderStub) UpdateAlbum(_ context.Context, draft operations.AlbumDraft) (operations.UpdateAlbumResult, error) {
	mapping := make(map[int]string, len(draft.Episodes))
	for _, episode := range draft.Episodes {
		mapping[episode.Sequence] = "7637437361307027476"
	}
	return operations.UpdateAlbumResult{Version: 2, EpisodeIDsBySequence: mapping}, nil
}

func (*publicationProviderStub) SubmitReview(context.Context, string, int, int) (string, error) {
	return "7637437361307027000", nil
}

func (*publicationProviderStub) QueryAlbum(context.Context, string, int) (operations.AlbumObservation, error) {
	return operations.AlbumObservation{ReviewStatus: 2}, nil
}

func (*publicationProviderStub) AuthorizeAlbum(context.Context, string, []string) error { return nil }

func (*publicationProviderStub) SetOnlineVersion(context.Context, string, int) error { return nil }

func (*publicationProviderStub) SetAlbumListed(context.Context, string, bool) error { return nil }
