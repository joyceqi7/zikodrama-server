package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"drama-server/internal/biz/application"
	"drama-server/internal/biz/operations"
	"gorm.io/gorm"
)

// TestConsumerUserScaleIntegration 用百万用户验证首页和深游标均按联合索引限量读取；需显式启用，禁止连接共享数据库运行。
func TestConsumerUserScaleIntegration(t *testing.T) {
	if os.Getenv("CONSUMER_USER_SCALE_TEST") != "1" {
		t.Skip("设置 CONSUMER_USER_SCALE_TEST=1 执行百万用户隔离测试")
	}
	f := newIAAFixture(t)
	digits := "(SELECT 0 AS n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)"
	numbers := "SELECT a.n+10*b.n+100*c.n+1000*d.n+10000*e.n AS n FROM " + digits + " a CROSS JOIN " + digits + " b CROSS JOIN " + digits + " c CROSS JOIN " + digits + " d CROSS JOIN " + digits + " e"
	for shard := 0; shard < 16; shard++ {
		// 既有库增量加索引后会保留旧 application_id 索引，验证优化器仍选择排序联合索引。
		if err := f.store.data.DB().Exec(fmt.Sprintf("ALTER TABLE users_%02d ADD INDEX idx_users_application (application_id)", shard)).Error; err != nil {
			t.Fatal(err)
		}
		statement := fmt.Sprintf("INSERT INTO users_%02d (id,bucket_id,application_id,created_at,updated_at) SELECT 9007199254740993+n*16+%d,%d,101,DATE_ADD('2025-01-01 00:00:00',INTERVAL FLOOR(n/4) SECOND),'2025-01-01 00:00:00' FROM (%s) AS sequence WHERE n<62500", shard, shard, shard, numbers)
		if err := f.store.data.DB().Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := application.WithContext(context.Background(), "101")
	for _, sample := range []struct {
		name  string
		query operations.ConsumerUserQuery
	}{
		{"首页", operations.ConsumerUserQuery{PageSize: 20}},
		{"约第900000条后的游标", operations.ConsumerUserQuery{PageSize: 20, AfterCreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Add(1562 * time.Second), AfterID: "9007199254840993"}},
	} {
		started := time.Now()
		result, err := f.store.ListConsumerUsers(ctx, sample.query)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s：耗时 %s，返回 %d 条，hasMore=%v", sample.name, time.Since(started), len(result.Items), result.HasMore)
		if len(result.Items) != 20 || !result.HasMore {
			t.Fatal("规模数据分页结果错误")
		}
		sql := f.store.data.DB().ToSQL(func(tx *gorm.DB) *gorm.DB {
			var items []operations.ConsumerUser
			return consumerUserListQuery(tx, "101", sample.query).Find(&items)
		})
		if strings.Contains(strings.ToUpper(sql), "OFFSET") || strings.Contains(strings.ToUpper(sql), "COUNT(") || strings.Count(sql, "LIMIT 21") != 17 {
			t.Fatalf("查询未限制各分片候选或包含全量统计/偏移：SQL 长度=%d，LIMIT 数=%d", len(sql), strings.Count(sql, "LIMIT 21"))
		}
		var plan string
		if err := f.store.data.DB().Raw("EXPLAIN ANALYZE " + sql).Row().Scan(&plan); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s执行计划：\n%s", sample.name, plan)
		for _, line := range strings.Split(plan, "\n") {
			if !strings.Contains(line, "idx_users_application_created_id") {
				continue
			}
			matches := regexp.MustCompile(`actual time=[^)]* rows=([0-9]+) loops=1`).FindStringSubmatch(line)
			if len(matches) != 2 {
				t.Fatal("执行计划缺少真实读取行数")
			}
			rows, err := strconv.Atoi(matches[1])
			if err != nil {
				t.Fatal(err)
			}
			if rows > 21 {
				t.Fatalf("分片索引扫描超过候选上限：%d", rows)
			}
		}
		if strings.Count(plan, "idx_users_application_created_id") != 16 || strings.Contains(plan, "Table scan on users_") {
			t.Fatal("分片查询未使用分页联合索引")
		}
	}
}
