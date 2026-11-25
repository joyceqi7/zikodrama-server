package consumer

import "testing"

// TestConfirmedAuthorizationCount 验证线上授权去重、排除拥有方和下架后不再计数。
func TestConfirmedAuthorizationCount(t *testing.T) {
	for _, test := range []struct {
		name  string
		drama *Drama
		want  int
	}{
		{"未上线", nil, 0},
		{"无其他授权", &Drama{PublishStatus: "published", OwnerApplicationID: "1"}, 0},
		{"去重并排除发布方", &Drama{PublishStatus: "published", OwnerApplicationID: "1", AuthorizedApplicationIDs: []string{"1", "2", "2", "", "3"}}, 2},
		{"已下架", &Drama{PublishStatus: "offline", AuthorizedApplicationIDs: []string{"2"}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.drama.ConfirmedAuthorizationCount(); got != test.want {
				t.Fatalf("授权数 = %d，期望 %d", got, test.want)
			}
		})
	}
}
