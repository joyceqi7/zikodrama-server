package consumer

import "testing"

// TestRewardedAdUnitForUID 验证按完整 UID 取模、超出 JS 安全整数和 uint64 的精度以及异常输入。
func TestRewardedAdUnitForUID(t *testing.T) {
	codes := []string{"ad-a", "ad-b", "ad-c"}
	for _, test := range []struct{ uid, want string }{
		{"0", "ad-a"}, {"1", "ad-b"}, {"2", "ad-c"}, {"3", "ad-a"},
		{"101006", "ad-c"}, {"9007199254740993", "ad-a"},
		{"18446744073709551615", "ad-a"}, {"18446744073709551616", "ad-b"},
	} {
		for range 3 {
			got, err := rewardedAdUnitForUID(test.uid, codes)
			if err != nil || got != test.want {
				t.Fatalf("UID %s 取模不符: %s %v", test.uid, got, err)
			}
		}
	}
	for _, uid := range []string{"", "-1", "+1", "1.5", " 1", "viewer"} {
		if _, err := rewardedAdUnitForUID(uid, codes); err == nil {
			t.Fatal("多候选分配必须拒绝非十进制 UID")
		}
	}
	if _, err := rewardedAdUnitForUID("1", nil); err == nil {
		t.Fatal("空集合不得产生广告位")
	}
	if got, err := rewardedAdUnitForUID("1", codes[:1]); err != nil || got != "ad-a" {
		t.Fatal("单广告位结果不符")
	}
}
