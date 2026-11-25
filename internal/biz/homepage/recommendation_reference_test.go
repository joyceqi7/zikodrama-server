package homepage

import "testing"

// TestRecommendationReferences 覆盖旧 ID、隐藏条目和移除后的下线门禁语义。
func TestRecommendationReferences(t *testing.T) {
	for _, config := range []Carousel{
		{DramaIDs: []string{"201"}},
		{Items: []CarouselItem{{DramaID: "201", Visible: false}}},
		{DramaIDs: []string{"202"}, Items: []CarouselItem{{DramaID: "201", Visible: true}}},
	} {
		if !config.ReferencesDrama("201") || config.ReferencesDrama("999") {
			t.Fatal("推荐引用判断错误")
		}
	}
	if (Carousel{}).ReferencesDrama("201") {
		t.Fatal("移除后仍有引用")
	}
}
