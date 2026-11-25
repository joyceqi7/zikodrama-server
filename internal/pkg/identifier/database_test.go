package identifier

import "testing"

// TestValidDatabaseID 覆盖 JavaScript 安全整数边界、MySQL 无符号上限和可能触发隐式数值转换的输入。
func TestValidDatabaseID(t *testing.T) {
	for _, id := range []string{"1", "9007199254740993", "18446744073709551615"} {
		if !ValidDatabaseID(id) {
			t.Errorf("合法主键被拒绝: %q", id)
		}
	}
	for _, id := range []string{"", "0", "01", "+1", "-1", "1e3", "1abc", " 1", "1 ", "18446744073709551616", "drama_123"} {
		if ValidDatabaseID(id) {
			t.Errorf("非法主键被接受: %q", id)
		}
	}
}
