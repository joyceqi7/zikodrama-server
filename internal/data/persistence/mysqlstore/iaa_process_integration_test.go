package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	domain "drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/monetization"
	mysqldriver "github.com/go-sql-driver/mysql"
)

// TestIAAServeProcess 是父测试启动的独立后端进程；不建库、不清库，不允许绑定公网。
func TestIAAServeProcess(t *testing.T) {
	dsn := os.Getenv("TEST_IAA_PROCESS_DSN")
	ready := os.Getenv("TEST_IAA_READY_FILE")
	if dsn == "" || ready == "" {
		t.Skip("仅供隔离集成测试的子进程使用")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil || host != "127.0.0.1" || !strings.HasPrefix(cfg.DBName, "drama_iaa_") {
		t.Fatal("子进程只能使用本机独立 IAA 数据库")
	}
	f := &iaaFixture{dsn: dsn}
	f.open(t)
	// 人工浏览器验收启用真实直连事务，平台调用仍使用隔离测试替身。
	if os.Getenv("TEST_IAA_BROWSER") == "1" {
		f.direct = directplay.NewService(NewDirectRepository(f.store), nil)
	}
	address := os.Getenv("TEST_IAA_PROCESS_ADDRESS")
	if address == "" {
		address = "127.0.0.1:0"
	}
	host, _, err = net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("本机替身后端只能监听 127.0.0.1")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: f.handler(t), ReadHeaderTimeout: 5 * time.Second}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	if err := os.WriteFile(ready, []byte("http://"+listener.Addr().String()), 0600); err != nil {
		t.Fatal(err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-signals:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}
}

// startIAAProcess 每次启动全新进程并使用原库，验证会话和权益不依赖进程内缓存。
func startIAAProcess(t *testing.T, f *iaaFixture, address string) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	log, err := os.OpenFile(filepath.Join(dir, "backend.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestIAAServeProcess$", "-test.timeout=30m")
	command.Env = append(os.Environ(), "TEST_IAA_PROCESS_DSN="+f.dsn, "TEST_IAA_READY_FILE="+ready, "TEST_IAA_PROCESS_ADDRESS="+address)
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = command.Process.Signal(os.Interrupt)
			if err := command.Wait(); err != nil {
				t.Errorf("后端进程退出异常: %v", err)
			}
			_ = log.Close()
		})
	}
	t.Cleanup(stop)
	for i := 0; i < 100; i++ {
		if raw, err := os.ReadFile(ready); err == nil {
			return string(raw), stop
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	t.Fatal("独立后端未在 5 秒内就绪；详见测试临时目录 backend.log")
	return "", stop
}

// TestIAAProcessRestartIntegration 真正退出后端进程后重启，保留原凭据、领取幂等键及共享 UID 游标。
func TestIAAProcessRestartIntegration(t *testing.T) {
	requireUIDIntegration(t)
	f := newIAAFixture(t)
	base, stop := startIAAProcess(t, f, "")
	token, user := iaaLogin(t, base, "101", "user-a")
	firstUID, err := strconv.ParseUint(user, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	watermark := uidWatermark(t, f)
	claim := iaaClaim(iaaPlay(t, base, token, 2), "process-restart")
	iaaRequest(t, base, "POST", "/api/v1/reward-grants", token, "", claim, 201)
	stop()
	base, _ = startIAAProcess(t, f, "")
	if iaaPlay(t, base, token, 3).Status != domain.PlayStatusPlayable {
		t.Fatal("真正重启后原凭据或权益失效")
	}
	_, again := iaaLogin(t, base, "101", "user-a")
	if again != user {
		t.Fatal("真正重启后同平台账号变成新访客")
	}
	_, freshUser := iaaLogin(t, base, "101", "user-b")
	if freshUser != strconv.FormatUint(firstUID+1, 10) || uidWatermark(t, f) != watermark {
		t.Fatal("真正重启后未继续共享 UID 游标，或重复申请了 MySQL 号段")
	}
	raw := iaaRequest(t, base, "POST", "/api/v1/reward-grants", token, "", claim, 200)
	var result monetization.ClaimResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != monetization.ClaimStatusAlreadyGranted || f.count(t, "reward_grants", "visitor_id = ?", user) != 1 {
		t.Fatal("跨进程重试产生重复奖励")
	}
}

// TestIAABrowserIntegration 为人工浏览器操作保留独立真实后端；Ctrl+C 清理，SIGUSR1 重启后端保留数据库。
// 仅 TEST_IAA_BROWSER=1 时启用；不属于默认 verify，也不会把本机替身编进产品二进制。
func TestIAABrowserIntegration(t *testing.T) {
	if os.Getenv("TEST_IAA_BROWSER") != "1" {
		t.Skip("未启用本机浏览器联调")
	}
	f := newIAAFixture(t)
	// 直连页面验收使用完整草稿和独立应用，不改任何开发或测试环境业务库。
	seedDirect(t, f)
	base, stop := startIAAProcess(t, f, "127.0.0.1:18080")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)
	defer signal.Stop(signals)
	fmt.Printf("IAA browser fixture ready: %s controller_pid=%d\n", base, os.Getpid())
	for sig := range signals {
		stop()
		if sig != syscall.SIGUSR1 {
			return
		}
		base, stop = startIAAProcess(t, f, "127.0.0.1:18080")
		fmt.Printf("IAA browser backend restarted: %s\n", base)
	}
}
