package operations

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOptionalMediaAccountInput 验证新增账号始终按字符串处理，未配置不妨碍原直连配置保存。
func TestOptionalMediaAccountInput(t *testing.T) {
	for _, test := range []struct {
		raw   string
		valid bool
		id    string
	}{
		{`{"access_key_id":"********"}`, true, ""},
		{`{"byteplus_account_id":""}`, true, ""},
		{`{"byteplus_account_id":" 0009007199254740993 "}`, true, "0009007199254740993"},
		{`{"byteplus_account_id":9007199254740993}`, false, ""},
		{`{"byteplus_account_id":null}`, false, ""},
		{`{"byteplus_account_id":"invalid!"}`, false, ""},
		{`{"byteplus_account_id":"` + strings.Repeat("1", 65) + `"}`, false, ""},
	} {
		encoded, err := normalizeMediaAccountInput(json.RawMessage(test.raw))
		if (err == nil) != test.valid {
			t.Fatalf("可选账号格式校验错误：valid=%v err=%v", test.valid, err)
		}
		if err == nil {
			var row struct {
				ID string `json:"byteplus_account_id"`
			}
			if err := json.Unmarshal(encoded, &row); err != nil || row.ID != test.id {
				t.Fatal("账号字符串或前导零改变", err)
			}
		}
	}
}
