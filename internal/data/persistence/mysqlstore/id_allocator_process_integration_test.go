package mysqlstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"drama-server/internal/biz/visitor"
	mysqldriver "github.com/go-sql-driver/mysql"
)

const uidProcessApplication = "101"

// uidProcessConfig 由父测试写入本机私有目录；子进程只打开本次创建的隔离库。
type uidProcessConfig struct {
	// Token 绑定本次父测试和子进程，禁止误用其他测试留下的配置。
	Token string `json:"token"`
	// Database 是父测试创建的完整数据库名。
	Database string `json:"database"`
	// Phase 区分首次运行和全部进程退出后的重启运行。
	Phase int `json:"phase"`
	// Instance 是同阶段内独立进程的编号。
	Instance int `json:"instance"`
	// Count 是本进程计划创建的新身份数量。
	Count int `json:"count"`
	// Warmup 是重启后暂停检查前先创建的新身份数量。
	Warmup int `json:"warmup"`
	// Existing 保存第一阶段已经落库的身份结果，复登必须返回相同主体。
	Existing []visitor.UserRef `json:"existing,omitempty"`
}

// uidProcessResult 只输出核验所需的结果，不输出连接串、凭据或补段租约。
type uidProcessResult struct {
	// PID 证明每个发号实例处于独立操作系统进程。
	PID int `json:"pid"`
	// CacheKey 用于证明所有独立实例共享同一 Redis 游标。
	CacheKey string `json:"cache_key"`
	// Users 按输入序号保存实际落库后返回的 UID 和逻辑桶。
	Users []visitor.UserRef `json:"users"`
	// ExistingLogins 记录已校验身份不变的旧用户登录次数。
	ExistingLogins int `json:"existing_logins"`
}

type uidProcess struct {
	config  uidProcessConfig
	path    string
	command *exec.Cmd
	output  bytes.Buffer
	done    chan struct{}
	err     error
}

// TestUIDMultiProcessPersistenceIntegration 通过本机八个真实进程验证发号、分片落库、预取和发布重启续号。
// MySQL 与 Redis 可以位于明确授权的测试环境；写入和清理仅限本次独立数据库及其 UID key。
func TestUIDMultiProcessPersistenceIntegration(t *testing.T) {
	requireUIDIntegration(t)
	f := newIAAFixtureWithSchema(t, uidProcessSchema(t), false)
	var tableCount int
	if err := f.store.data.pool.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'").Scan(&tableCount); err != nil {
		t.Fatalf("核验隔离测试表数量失败：%v", err)
	}
	if tableCount != 17 || uidWatermark(t, f) != 1 {
		t.Fatalf("隔离测试必须来自生产定义的 17 张表及初始号段种子：tables=%d", tableCount)
	}
	setUIDSegment(t, f, 1, 256)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	key, err := f.store.ids.cacheKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir, token := t.TempDir(), rand.Text()
	first := startUIDProcesses(t, ctx, f, dir, token, 1, 568, 0, nil)
	releaseUIDProcesses(t, first, "start")
	firstResults := collectUIDProcesses(t, ctx, first, "result", true)
	assertUIDProcessResults(t, first, firstResults, key)
	assertUIDProcessPersistence(t, ctx, f, key, [][]uidProcessResult{firstResults}, 2272)
	t.Log("第一阶段所有子进程已退出；当前号段保留 32 个 UID，备用段保留 256 个 UID")

	second := startUIDProcesses(t, ctx, f, dir, token, 2, 600, 2, firstResults)
	releaseUIDProcesses(t, second, "start")
	loggedIn := collectUIDProcesses(t, ctx, second, "logged-in", false)
	assertUIDProcessResults(t, second, loggedIn, key)
	for _, result := range loggedIn {
		if result.ExistingLogins != 8 || len(result.Users) != 0 {
			t.Fatalf("复登检查点出现新发号或旧身份缺失：pid=%d，复登=%d，新用户=%d", result.PID, result.ExistingLogins, len(result.Users))
		}
	}
	assertUIDProcessPersistence(t, ctx, f, key, [][]uidProcessResult{firstResults}, 2272)
	t.Log("重启后 32 次旧身份登录已完成；真实 Redis 游标与 MySQL 水位均未变化")
	releaseUIDProcesses(t, second, "logins-checked")
	warmup := collectUIDProcesses(t, ctx, second, "warmup", false)
	assertUIDProcessResults(t, second, warmup, key)
	oldPIDs := make(map[int]bool)
	for _, result := range firstResults {
		oldPIDs[result.PID] = true
	}
	for _, result := range warmup {
		if oldPIDs[result.PID] || result.ExistingLogins != 8 || len(result.Users) != 2 {
			t.Fatalf("重启阶段没有使用新 PID、复登 8 个旧用户或仅创建 2 个新用户：pid=%d，复登=%d，新用户=%d", result.PID, result.ExistingLogins, len(result.Users))
		}
	}
	assertUIDProcessPersistence(t, ctx, f, key, [][]uidProcessResult{firstResults, warmup}, 2280)
	t.Log("重启后 4 个新进程完成 32 次旧身份登录和 8 次新用户创建；MySQL 水位仍为 2561，Redis 仅消费 8 个 UID")
	releaseUIDProcesses(t, second, "continue")
	secondResults := collectUIDProcesses(t, ctx, second, "result", true)
	assertUIDProcessResults(t, second, secondResults, key)
	assertUIDProcessPersistence(t, ctx, f, key, [][]uidProcessResult{firstResults, secondResults}, 4672)
}

// TestUIDAllocatorProcessChild 仅由上述父测试启动；不建库、不清库，不监听任何端口。
func TestUIDAllocatorProcessChild(t *testing.T) {
	if os.Getenv("TEST_UID_PROCESS_CHILD") != "1" {
		t.Skip("仅供 UID 多进程集成测试的子进程使用")
	}
	path := os.Getenv("TEST_UID_PROCESS_CONFIG")
	var cfg uidProcessConfig
	readUIDProcessJSON(t, path, &cfg)
	dsn := os.Getenv("TEST_UID_PROCESS_DSN")
	dbConfig, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("子进程数据库连接配置无效")
	}
	if len(cfg.Token) < 20 || cfg.Token != os.Getenv("TEST_UID_PROCESS_TOKEN") ||
		!strings.HasPrefix(cfg.Database, "drama_iaa_") || cfg.Database != dbConfig.DBName ||
		!filepath.IsAbs(path) || cfg.Instance < 0 || cfg.Instance >= 4 ||
		cfg.Phase < 1 || cfg.Phase > 2 || cfg.Count < 1 || cfg.Count > 1000 || cfg.Warmup < 0 || cfg.Warmup > cfg.Count {
		t.Fatal("子进程只允许操作父测试明确指定的本次隔离数据库和测试范围")
	}
	f := &iaaFixture{dsn: dsn}
	f.open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	key, err := f.store.ids.cacheKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result := uidProcessResult{PID: os.Getpid(), CacheKey: key, Users: make([]visitor.UserRef, cfg.Count)}
	writeUIDProcessJSON(t, path+".ready", result.PID)
	waitUIDProcessFile(t, ctx, filepath.Join(filepath.Dir(path), fmt.Sprintf("phase-%d-start", cfg.Phase)))
	for index, want := range cfg.Existing {
		got, err := f.store.ResolveTikTokIdentityRef(ctx, uidProcessIdentity(1, cfg.Instance, index))
		if err != nil || got != want {
			t.Fatalf("重启后恢复旧身份失败：index=%d，错误=%v", index, err)
		}
		result.ExistingLogins++
	}
	if len(cfg.Existing) > 0 {
		checkpoint := result
		checkpoint.Users = nil
		writeUIDProcessJSON(t, path+".logged-in", checkpoint)
		waitUIDProcessFile(t, ctx, filepath.Join(filepath.Dir(path), fmt.Sprintf("phase-%d-logins-checked", cfg.Phase)))
	}
	create := func(begin, end int) {
		var workers sync.WaitGroup
		failures := make(chan error, 4)
		for worker := range 4 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for index := begin + worker; index < end; index += 4 {
					user, err := f.store.ResolveTikTokIdentityRef(ctx, uidProcessIdentity(cfg.Phase, cfg.Instance, index))
					if err != nil {
						failures <- fmt.Errorf("新身份落库失败，index=%d: %w", index, err)
						return
					}
					result.Users[index] = user
				}
			}()
		}
		workers.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		if t.Failed() {
			t.FailNow()
		}
		waitUIDProcessRefill(t, ctx, f, key)
	}
	if cfg.Warmup > 0 {
		create(0, cfg.Warmup)
		checkpoint := result
		checkpoint.Users = result.Users[:cfg.Warmup]
		writeUIDProcessJSON(t, path+".warmup", checkpoint)
		waitUIDProcessFile(t, ctx, filepath.Join(filepath.Dir(path), fmt.Sprintf("phase-%d-continue", cfg.Phase)))
	}
	create(cfg.Warmup, cfg.Count)
	writeUIDProcessJSON(t, path+".result", result)
}

// uidProcessIdentity 生成仅属于本测试隔离库的确定性身份，便于跨进程重新登录及逐行核对。
func uidProcessIdentity(phase, instance, index int) visitor.TikTokIdentityRecord {
	digest := sha256.Sum256([]byte(fmt.Sprintf("uid-process/%d/%d/%d", phase, instance, index)))
	return visitor.TikTokIdentityRecord{ApplicationID: uidProcessApplication, OpenIDHash: fmt.Sprintf("%x", digest)}
}

// uidProcessSchema 直接抽取生产初始化脚本中的 17 张发号/用户表及水位种子，避免远端联调创建无关业务表。
// 表定义只有生产脚本这一份；缺表、重复定义或种子变化会在连接测试环境前直接失败。
func uidProcessSchema(t *testing.T) string {
	t.Helper()
	wanted := map[string]bool{"id_allocators": true}
	for shard := range 16 {
		wanted[fmt.Sprintf("users_%02d", shard)] = true
	}
	found := make(map[string]bool, len(wanted))
	var selected []string
	seeds := 0
	for _, statement := range strings.Split(string(readDeploymentSchema(t)), ";") {
		var lines []string
		for _, line := range strings.Split(statement, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				lines = append(lines, line)
			}
		}
		sql := strings.TrimSpace(strings.Join(lines, "\n"))
		if strings.HasPrefix(sql, "INSERT INTO `id_allocators` ") {
			seeds++
			selected = append(selected, statement)
			continue
		}
		for table := range wanted {
			if strings.HasPrefix(sql, "CREATE TABLE `"+table+"` (") {
				if found[table] {
					t.Fatalf("生产初始化脚本包含重复 UID 测试表：%s", table)
				}
				found[table] = true
				selected = append(selected, statement)
				break
			}
		}
	}
	if len(found) != 17 || seeds != 1 {
		t.Fatalf("生产脚本缺少 UID 集成测试需要的结构或种子：tables=%d，seeds=%d", len(found), seeds)
	}
	return strings.Join(selected, ";") + ";"
}

func startUIDProcesses(t *testing.T, ctx context.Context, f *iaaFixture, dir, token string, phase, count, warmup int, previous []uidProcessResult) []*uidProcess {
	t.Helper()
	dbConfig, err := mysqldriver.ParseDSN(f.dsn)
	if err != nil {
		t.Fatal("父测试数据库连接配置无效")
	}
	var processes []*uidProcess
	for instance := range 4 {
		cfg := uidProcessConfig{Token: token, Database: dbConfig.DBName, Phase: phase, Instance: instance, Count: count, Warmup: warmup}
		if previous != nil {
			cfg.Existing = previous[instance].Users[:8]
		}
		path := filepath.Join(dir, fmt.Sprintf("phase-%d-instance-%d.json", phase, instance))
		writeUIDProcessJSON(t, path, cfg)
		childCtx, childCancel := context.WithCancel(ctx)
		p := &uidProcess{config: cfg, path: path, done: make(chan struct{})}
		p.command = exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestUIDAllocatorProcessChild$", "-test.timeout=9m")
		p.command.Env = append(os.Environ(), "TEST_UID_PROCESS_CHILD=1", "TEST_UID_PROCESS_CONFIG="+path, "TEST_UID_PROCESS_DSN="+f.dsn, "TEST_UID_PROCESS_TOKEN="+token)
		p.command.Stdout, p.command.Stderr = &p.output, &p.output
		if err := p.command.Start(); err != nil {
			childCancel()
			t.Fatal(err)
		}
		go func() { p.err = p.command.Wait(); close(p.done) }()
		t.Cleanup(func() { childCancel(); <-p.done })
		processes = append(processes, p)
	}
	for _, p := range processes {
		waitUIDProcessCheckpoint(t, ctx, p, "ready")
	}
	return processes
}

func releaseUIDProcesses(t *testing.T, processes []*uidProcess, stage string) {
	t.Helper()
	first := processes[0]
	path := filepath.Join(filepath.Dir(first.path), fmt.Sprintf("phase-%d-%s", first.config.Phase, stage))
	if err := os.WriteFile(path, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
}

func collectUIDProcesses(t *testing.T, ctx context.Context, processes []*uidProcess, stage string, stopped bool) []uidProcessResult {
	t.Helper()
	results := make([]uidProcessResult, len(processes))
	for index, p := range processes {
		waitUIDProcessCheckpoint(t, ctx, p, stage)
		if stopped {
			select {
			case <-p.done:
				if p.err != nil {
					failUIDProcess(t, p)
				}
			case <-ctx.Done():
				t.Fatal("等待 UID 子进程退出超时")
			}
		}
		readUIDProcessJSON(t, p.path+"."+stage, &results[index])
	}
	return results
}

func waitUIDProcessCheckpoint(t *testing.T, ctx context.Context, p *uidProcess, stage string) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(p.path + "." + stage); err == nil {
			return
		}
		select {
		case <-p.done:
			// 文件写完后进程可能立即正常退出，重新读一次消除完成事件与文件检查之间的竞争。
			if _, err := os.Stat(p.path + "." + stage); err == nil && p.err == nil {
				return
			}
			failUIDProcess(t, p)
		case <-ctx.Done():
			t.Fatalf("等待 UID 子进程检查点超时：phase=%d，instance=%d，stage=%s", p.config.Phase, p.config.Instance, stage)
		case <-ticker.C:
		}
	}
}

func failUIDProcess(t *testing.T, p *uidProcess) {
	t.Helper()
	output := p.output.String()
	// 第三方驱动错误也不得把环境变量中的测试凭据带入父进程输出。
	for _, value := range []string{os.Getenv("TEST_MYSQL_DSN"), os.Getenv("TEST_REDIS_PASSWORD"), p.config.Token} {
		if value != "" {
			output = strings.ReplaceAll(output, value, "[REDACTED]")
		}
	}
	if cfg, err := mysqldriver.ParseDSN(os.Getenv("TEST_MYSQL_DSN")); err == nil && cfg.Passwd != "" {
		output = strings.ReplaceAll(output, cfg.Passwd, "[REDACTED]")
	}
	if len(output) > 12000 {
		output = output[len(output)-12000:]
	}
	t.Fatalf("UID 子进程异常：phase=%d，instance=%d，pid=%d，错误=%v\n%s", p.config.Phase, p.config.Instance, p.command.Process.Pid, p.err, output)
}

func waitUIDProcessFile(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("等待父进程发号屏障超时")
		case <-ticker.C:
		}
	}
}

func writeUIDProcessJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".tmp", raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}

func readUIDProcessJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, value); err != nil {
		t.Fatal(err)
	}
}

// waitUIDProcessRefill 在进程退出或写检查点前等待真实共享租约释放，避免切断仍在发布的后台预取。
func waitUIDProcessRefill(t *testing.T, ctx context.Context, f *iaaFixture, key string) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		pending, err := f.uidClient.HGet(waitCtx, key, "pending_token").Result()
		if err != nil {
			t.Fatalf("读取 UID 预取状态失败：%v", err)
		}
		if pending == "" {
			return
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("UID 后台预取未在检查点前完成")
		case <-ticker.C:
		}
	}
}

func assertUIDProcessResults(t *testing.T, processes []*uidProcess, results []uidProcessResult, key string) {
	t.Helper()
	pids := make(map[int]bool)
	for index, result := range results {
		if result.PID == os.Getpid() || pids[result.PID] || result.PID != processes[index].command.Process.Pid || result.CacheKey != key {
			t.Fatalf("未观察到独立 PID 和统一 Redis key：instance=%d，pid=%d", index, result.PID)
		}
		pids[result.PID] = true
		t.Logf("phase=%d instance=%d pid=%d goroutines=4 new_users=%d existing_logins=%d shared_key=%s", processes[index].config.Phase, index, result.PID, len(result.Users), result.ExistingLogins, result.CacheKey)
	}
}

// assertUIDProcessPersistence 同时核对子进程结果、16 张真实用户表、MySQL 水位和静止后的 Redis 完整段状态。
func assertUIDProcessPersistence(t *testing.T, ctx context.Context, f *iaaFixture, key string, phases [][]uidProcessResult, count int) {
	t.Helper()
	waitUIDProcessRefill(t, ctx, f, key)
	type expectedUser struct {
		ref      visitor.UserRef
		identity visitor.TikTokIdentityRecord
	}
	expected := make(map[string]expectedUser, count)
	for phase, results := range phases {
		for instance, result := range results {
			for index, user := range result.Users {
				if _, exists := expected[user.ID]; exists || user.ID == "" || user.AuthVersion != 1 {
					t.Fatalf("进程返回重复或无效 UID：%s", user.ID)
				}
				expected[user.ID] = expectedUser{ref: user, identity: uidProcessIdentity(phase+1, instance, index)}
			}
		}
	}
	if len(expected) != count {
		t.Fatalf("子进程返回用户数=%d，期望=%d", len(expected), count)
	}
	for id := 1; id <= count; id++ {
		if _, exists := expected[strconv.Itoa(id)]; !exists {
			t.Fatalf("正常发号存在跳段或缺失 UID：%d", id)
		}
	}
	var union []string
	for shard := range 16 {
		union = append(union, fmt.Sprintf("SELECT id FROM users_%02d", shard))
	}
	var rowsCount, distinctCount, minimum, maximum uint64
	query := "SELECT COUNT(*), COUNT(DISTINCT id), MIN(id), MAX(id) FROM (" + strings.Join(union, " UNION ALL ") + ") AS all_users"
	if err := f.store.data.pool.QueryRowContext(ctx, query).Scan(&rowsCount, &distinctCount, &minimum, &maximum); err != nil {
		t.Fatalf("聚合真实用户分片失败：%v", err)
	}
	if rowsCount != uint64(count) || distinctCount != uint64(count) || minimum != 1 || maximum != uint64(count) {
		t.Fatalf("跨分片落库不完整或 UID 重复：rows=%d distinct=%d min=%d max=%d want=%d", rowsCount, distinctCount, minimum, maximum, count)
	}
	for shard := range 16 {
		rows, err := f.store.data.pool.QueryContext(ctx, fmt.Sprintf("SELECT id, bucket_id, application_id, open_id_hash FROM users_%02d", shard))
		if err != nil {
			t.Fatal(err)
		}
		shardCount := 0
		for rows.Next() {
			var id, app, hash string
			var bucket uint16
			if err := rows.Scan(&id, &bucket, &app, &hash); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			want, exists := expected[id]
			if !exists || want.ref.BucketID != bucket || int(bucket%16) != shard || want.identity.ApplicationID != app || want.identity.OpenIDHash != hash {
				_ = rows.Close()
				t.Fatalf("真实用户行与子进程返回的身份或分片不一致：UID=%s，shard=%d", id, shard)
			}
			shardCount++
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil || shardCount == 0 {
			t.Fatalf("用户分片未覆盖或查询失败：shard=%d，rows=%d，错误=%v", shard, shardCount, err)
		}
	}
	state, err := f.uidClient.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	start := ((count - 1) / 256 * 256) + 1
	used := count - start + 1
	standbyStart, standbySize := "", 0
	if used*5 >= 256*4 {
		standbyStart, standbySize = strconv.Itoa(start+256), 256
	}
	wantState := map[string]string{"schema": "1", "start": strconv.Itoa(start), "size": "256", "used": strconv.Itoa(used), "standby_start": standbyStart, "standby_size": strconv.Itoa(standbySize), "pending_token": "", "pending_until": "0", "retry_at": "0"}
	for field, want := range wantState {
		if got, exists := state[field]; !exists || got != want {
			// 租约值属于内部令牌，即使不符合期望也只输出字段名。
			t.Fatalf("真实 Redis 状态与已落库 UID 数不匹配：field=%s", field)
		}
	}
	watermark := uidWatermark(t, f)
	if watermark != uint64(start+256+standbySize) || maximum >= watermark {
		t.Fatalf("MySQL 预留水位与 Redis 当前段/备用段不一致：watermark=%d，expected=%d，max_uid=%d", watermark, start+256+standbySize, maximum)
	}
	t.Logf("核验通过：mysql_rows=%d distinct_uids=%d uid_range=%d..%d shards=16 mysql_next_id=%d redis_start=%d used=%d size=256 standby_start=%s standby_size=%d", rowsCount, distinctCount, minimum, maximum, watermark, start, used, standbyStart, standbySize)
}
