package application

import (
	"context"
	"testing"
)

func TestMiniReviewMatchesExactVersionAndMode(t *testing.T) {
	config := MiniReview{Version: "1.2.3", Enabled: true, DramaIDs: []string{"12"}, Revision: 4}
	for _, item := range []struct {
		version         string
		mode            MonetizationMode
		active, episode bool
	}{
		{"1.2.3", MonetizationModeIAA, true, true},
		{"1.2.3", MonetizationModeIAP, true, false},
		{"1.2.3", MonetizationModeHybrid, true, false},
		{"1.2.4", MonetizationModeIAA, false, false},
		{"", MonetizationModeIAA, false, false},
	} {
		ctx := WithMiniReview(WithMiniVersion(context.Background(), item.version), config, item.mode)
		state := MiniReviewFromContext(ctx)
		if state.Enabled != item.active || state.EpisodeOnly != item.episode || state.Revision != 4 {
			t.Fatalf("%+v: %+v", item, state)
		}
		state.DramaIDs[0] = "other"
		if config.DramaIDs[0] != "12" {
			t.Fatal("请求修改污染权威清单")
		}
	}
	config.Enabled = false
	if MiniReviewFromContext(WithMiniReview(WithMiniVersion(context.Background(), "1.2.3"), config, MonetizationModeIAA)).Enabled {
		t.Fatal("关闭后仍命中审核")
	}
}

func TestMiniVersionValidation(t *testing.T) {
	for _, version := range []string{"0.0.0", "1.20.300"} {
		if !ValidMiniVersion(version) {
			t.Fatalf("拒绝合法版本 %s", version)
		}
	}
	for _, version := range []string{"", "v1.0.0", "01.0.0", "1.0", "1.0.0-beta", "1.0.0 ", "1.0.0\n"} {
		if ValidMiniVersion(version) {
			t.Fatalf("接受非法版本 %q", version)
		}
	}
}

func TestMiniReviewMultipleVersionsShareWhitelist(t *testing.T) {
	legacy := MiniReview{Version: "1.0.0", Enabled: true, DramaIDs: []string{"12"}, Revision: 2}
	config := legacy.WithSettings(MiniReview{Versions: []MiniReviewVersion{{Version: "1.0.0", Enabled: false}, {Version: "1.1.0", Enabled: true}, {Version: "1.2.0", Enabled: true}}, Revision: 2})
	if !config.AnyEnabled() || config.Enabled || len(config.DramaIDs) != 1 {
		t.Fatal("多版本没有保留应用白名单或任一版本开启语义", config)
	}
	for _, version := range []string{"1.1.0", "1.2.0"} {
		ctx := WithMiniReview(WithMiniVersion(t.Context(), version), config, MonetizationModeIAA)
		if state := MiniReviewFromContext(ctx); !state.Enabled || !state.EpisodeOnly || state.DramaIDs[0] != "12" {
			t.Fatal("开启版本未共用白名单与 IAA 覆盖", version, state)
		}
	}
	for _, version := range []string{"1.0.0", "2.0.0", ""} {
		ctx := WithMiniReview(WithMiniVersion(t.Context(), version), config, MonetizationModeIAA)
		if MiniReviewFromContext(ctx).Enabled {
			t.Fatal("关闭或未登记版本被覆盖", version)
		}
	}
	// 旧后台保存某个版本不能删除新后台维护的其他版本。
	updated := config.WithSettings(MiniReview{Version: "1.1.0", Enabled: false, Revision: 2})
	if len(updated.Versions) != 3 || !updated.AnyEnabled() || updated.Versions[1].Enabled || !updated.Versions[2].Enabled {
		t.Fatal("旧单版本写入覆盖其他版本", updated)
	}
	updated.Versions[2].Enabled = false
	updated.DramaIDs[0] = "other"
	if !config.Versions[2].Enabled || config.DramaIDs[0] != "12" {
		t.Fatal("合并结果污染之前的配置")
	}
}
