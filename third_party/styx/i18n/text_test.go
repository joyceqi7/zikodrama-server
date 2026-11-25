package i18n

import (
	"context"
	"encoding/json"
	"testing"
)

func TestTextMapGetUsesRequestedBaseAndEnglishFallback(t *testing.T) {
	t.Parallel()
	texts := TextMap{
		"en":    "English",
		"fr":    "Français",
		"pt-BR": "Português",
	}
	for locale, want := range map[string]string{
		"fr-FR": "Français",
		"pt-BR": "Português",
		"de-DE": "English",
		"":      "English",
	} {
		if got := texts.Get(locale); got != want {
			t.Fatalf("TextMap.Get(%q) = %q, want %q", locale, got, want)
		}
	}
}

func TestTextMapPersistsAsJSON(t *testing.T) {
	t.Parallel()
	original := TextMap{"en": "English", "zh": "中文"}
	value, err := original.Value()
	if err != nil {
		t.Fatal(err)
	}
	data, ok := value.([]byte)
	if !ok || !json.Valid(data) {
		t.Fatalf("TextMap.Value() = %#v", value)
	}
	var restored TextMap
	if err := restored.Scan(data); err != nil {
		t.Fatal(err)
	}
	if restored["en"] != "English" || restored["zh"] != "中文" {
		t.Fatalf("TextMap.Scan() = %#v", restored)
	}
}

func TestTextMapScanRejectsInvalidDatabaseValues(t *testing.T) {
	t.Parallel()
	var texts TextMap
	if err := texts.Scan(42); err == nil {
		t.Fatal("TextMap.Scan() accepted an unsupported database value")
	}
	if err := texts.Scan([]byte("not-json")); err == nil {
		t.Fatal("TextMap.Scan() accepted invalid JSON")
	}
}

func TestTextMapSkipsEmptyValuesAndCanonicalizesKeys(t *testing.T) {
	t.Parallel()
	texts := TextMap{"zh_hans": "简体中文", "fr": "", "en": "English"}
	if got := texts.Get("zh-Hans-CN"); got != "简体中文" {
		t.Fatalf("TextMap.Get() = %q, want 简体中文", got)
	}
	if got := texts.Get("fr"); got != "English" {
		t.Fatalf("empty translation did not fall back: %q", got)
	}
}

func TestTextFromContextUsesRequestedLocaleOutsideMessageCatalog(t *testing.T) {
	t.Parallel()
	ctx := WithLocale(context.Background(), "fr-FR")
	if got := TextFromContext(ctx, map[string]string{"fr": "Français", "en": "English"}); got != "Français" {
		t.Fatalf("TextFromContext() = %q, want Français", got)
	}
}
