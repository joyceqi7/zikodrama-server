package operations_test

import (
	"testing"

	"drama-server/internal/biz/operations"
)

func TestDramaMetadataCatalogMatchesCurrentTikTokContract(t *testing.T) {
	t.Parallel()

	catalog := operations.DramaMetadataCatalog()
	if catalog.SchemaVersion != "2026-07-13" {
		t.Fatalf("unexpected catalog version: %q", catalog.SchemaVersion)
	}
	if len(catalog.Statuses) != 3 || len(catalog.Languages) != 19 || len(catalog.DramaTypes) != 4 || len(catalog.Tags) != 45 {
		t.Fatalf("unexpected catalog sizes: statuses=%d languages=%d types=%d tags=%d", len(catalog.Statuses), len(catalog.Languages), len(catalog.DramaTypes), len(catalog.Tags))
	}
	if catalog.Statuses[0].Value != operations.DramaStatusUnpublished || catalog.Statuses[1].Value != operations.DramaStatusUpdating || catalog.Statuses[2].Value != operations.DramaStatusCompleted {
		t.Fatalf("unexpected drama statuses: %+v", catalog.Statuses)
	}
	languages := make(map[string]bool, len(catalog.Languages))
	for _, language := range catalog.Languages {
		languages[language.Value] = true
	}
	for _, expected := range []string{"ar", "de", "en", "es", "fr", "id", "it", "ja", "kk", "ko", "pl", "pt", "ro", "ru", "th", "tr", "ug", "vi", "zh"} {
		if !languages[expected] {
			t.Fatalf("missing language %q", expected)
		}
	}
	tags := make(map[string]bool, len(catalog.Tags))
	for _, tag := range catalog.Tags {
		tags[tag.ID] = true
	}
	if tags["32"] || !tags["31"] || !tags["33"] || !tags["46"] {
		t.Fatalf("official tag gap or boundary is wrong: %+v", tags)
	}
}
