package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDeployRunsUpdatedScript 在隔离目录替换 Git、Docker 和 SSH，不接触服务器或用户 Agent。
func TestDeployRunsUpdatedScript(t *testing.T) {
	tests := []struct {
		name string
		mode string
		env  []string
		fail bool
		want []string
		omit []string
	}{
		{name: "使用配置文件和运维SSH文件构建", mode: "test", want: []string{"git <pull> <--ff-only> <origin> <test>", "docker <build> <--ssh> <default=", "deployment key> <--secret> <type=file,id=gitee_known_hosts,src=", "<--network> <bridge> <-p> <18080:18080>"}, omit: []string{"<--env> <DRAMA_LOG_", "<-print-log-directory>"}},
		{name: "私钥缺失由Docker返回错误", mode: "test", env: []string{"DEPLOY_TEST_MISSING_KEY=1"}, fail: true, want: []string{"docker <build>"}, omit: []string{"docker <rm>"}},
		{name: "主机文件缺失由Docker返回错误", mode: "test", env: []string{"DEPLOY_TEST_MISSING_HOSTS=1"}, fail: true, want: []string{"docker <build>"}, omit: []string{"docker <rm>"}},
		{name: "构建失败时保留原容器", mode: "test", env: []string{"DEPLOY_TEST_FAIL=build"}, fail: true, want: []string{"docker <build>"}, omit: []string{"docker <rm>", "docker <run>"}},
		{name: "日志目录授权失败时保留原容器", mode: "test", env: []string{"DEPLOY_TEST_FAIL=log_prepare"}, fail: true, want: []string{"<--user> <root>", "<chown -R app /app/logs && chmod -R u+rwX /app/logs>"}, omit: []string{"docker <rm> <-f> <drama-server>"}},
		{name: "日志环境变量不传入容器", mode: "test", env: []string{"DRAMA_LOG_DIRECTORY=/tmp/ignored", "DRAMA_LOG_ROTATION=week", "DRAMA_LOG_RETENTION_DAYS=0"}, omit: []string{"<--env> <DRAMA_LOG_", "dst=/tmp/ignored"}},
		{name: "代码拉取失败不进入构建", mode: "test", env: []string{"DEPLOY_TEST_FAIL=pull"}, fail: true, want: []string{"git <pull>"}, omit: []string{"docker <build>", "docker <rm>"}},
		{name: "生产使用main分支和host网络", mode: "prod", want: []string{"git <pull> <--ff-only> <origin> <main>", "<--network> <host>", "config.prod.yaml:/data/conf/config.yaml:ro>"}, omit: []string{"<18080:18080>"}},
		{name: "启动退出时不报告成功", mode: "test", env: []string{"DEPLOY_TEST_FAIL=startup"}, fail: true, want: []string{"docker <logs> <--tail> <50>"}},
		{name: "无效环境不拉取代码", mode: "invalid", fail: true, omit: []string{"git <fetch>", "docker <rm>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newDeployFixture(t)
			output, err := fixture.run(tt.mode, tt.env...)
			if (err != nil) != tt.fail {
				t.Fatalf("部署结果不符：%v\n%s", err, output)
			}
			trace := fixture.trace()
			if !tt.fail && !strings.Contains(trace, "<-v> <"+filepath.Join(fixture.root, "logs")+":/app/logs>") {
				t.Errorf("部署未挂载仓库日志目录：\n%s", trace)
			}
			for _, command := range []string{"git <fetch>", "git <checkout>", "git <pull>", "docker <build>"} {
				if strings.Count(trace, command) > 1 {
					t.Errorf("单次部署重复执行了 %q\n%s", command, trace)
				}
			}
			for _, want := range tt.want {
				if !strings.Contains(trace, want) {
					t.Errorf("缺少预期行为 %q\n%s", want, trace)
				}
			}
			for _, omitted := range append(tt.omit, "ssh-agent", "ssh-add", "ssh <-G>", "git <ls-remote>", "docker <info>", "nginx", "systemctl", "install") {
				if strings.Contains(trace, omitted) {
					t.Errorf("出现禁止行为 %q\n%s", omitted, trace)
				}
			}
			if tt.fail && strings.Contains(output, "部署成功") {
				t.Fatal("失败路径报告了部署成功")
			}
		})
	}
}

// TestDeployMountsRepositoryLogs 验证部署只挂载仓库日志目录，不额外探测配置。
func TestDeployMountsRepositoryLogs(t *testing.T) {
	fixture := newDeployFixture(t)
	output, err := fixture.run("test")
	if err != nil {
		t.Fatalf("相对路径部署失败：%v\n%s", err, output)
	}
	logDirectory := filepath.Join(fixture.root, "logs")
	trace := fixture.trace()
	wantMount := "<-v> <" + logDirectory + ":/app/logs>"
	if strings.Count(trace, wantMount) != 2 {
		t.Fatalf("授权和部署容器都应挂载仓库日志目录：\n%s", trace)
	}
	if strings.Contains(trace, "<-print-log-directory>") || strings.Contains(trace, "<--user> <app>") {
		t.Fatalf("部署脚本不应探测日志配置：\n%s", trace)
	}
	if prepare, replace := strings.Index(trace, "<chown -R app /app/logs && chmod -R u+rwX /app/logs>"), strings.Index(trace, "docker <rm> <-f> <drama-server>"); prepare < 0 || replace < 0 || prepare > replace {
		t.Fatalf("日志目录必须先授权，再替换旧容器：\n%s", trace)
	}
}

// TestDeployReloadsItselfAfterPull 验证入口被 Git 改写后使用新构建参数，且只拉取和构建一次。
func TestDeployReloadsItselfAfterPull(t *testing.T) {
	fixture := newDeployFixture(t)
	contents, err := os.ReadFile(filepath.Join(fixture.root, "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(contents), "app_name=drama-server", "app_name=updated-server", 1)
	writeDeployFile(t, filepath.Join(fixture.state, "updated-deploy.sh"), updated, 0755)
	output, err := fixture.run("test", "DEPLOY_TEST_REPLACE_SCRIPT=1")
	if err != nil {
		t.Fatalf("更新后部署失败：%v\n%s", err, output)
	}
	trace := fixture.trace()
	if !strings.Contains(trace, "<-t> <updated-server:test-abc1234>") || strings.Contains(trace, "<-t> <drama-server:test-abc1234>") {
		t.Fatalf("未使用刚拉取的新构建参数：\n%s", trace)
	}
	for _, command := range []string{"git <fetch>", "git <checkout>", "git <pull>", "docker <build>"} {
		if strings.Count(trace, command) != 1 {
			t.Errorf("更新后 %q 必须恰好执行一次\n%s", command, trace)
		}
	}
}

// TestDockerBuildUsesOperatorHostKey 确保下载和编译均消费部署传入的主机文件，不把公钥固化到镜像。
func TestDockerBuildUsesOperatorHostKey(t *testing.T) {
	dockerfile, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(dockerfile), "--mount=type=secret,id=gitee_known_hosts,") != 2 || strings.Contains(string(dockerfile), "COPY deploy/gitee_known_hosts") {
		t.Fatal("两个构建步骤必须临时读取运维提供的 known_hosts")
	}
	if _, err := os.Stat("../deploy/gitee_known_hosts"); !os.IsNotExist(err) {
		t.Fatal("仓库不应保存 Gitee 主机公钥")
	}
}

type deployFixture struct {
	t     *testing.T
	root  string
	state string
	bin   string
}

func newDeployFixture(t *testing.T) deployFixture {
	t.Helper()
	base := t.TempDir()
	fixture := deployFixture{t: t, root: filepath.Join(base, "repo with spaces"), state: filepath.Join(base, "state"), bin: filepath.Join(base, "fake-bin")}
	for _, path := range []string{fixture.root, fixture.state, fixture.bin} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"deploy.sh", "Dockerfile"} {
		contents, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		writeDeployFile(t, filepath.Join(fixture.root, name), string(contents), 0755)
	}
	for _, name := range []string{"config.test.yaml", "config.prod.yaml"} {
		writeDeployFile(t, filepath.Join(fixture.root, "configs", name), "otel:\n  log:\n    file:\n      directory: ./logs\n      prefix: drama-server\n      rotation: hour\n      retention_days: 3\n", 0600)
	}
	writeDeployFile(t, filepath.Join(fixture.state, "known hosts"), "mock host record\n", 0600)
	writeDeployFile(t, filepath.Join(fixture.state, "deployment key"), "mock identity; not a private key\n", 0600)
	for _, name := range []string{"git", "docker", "ssh", "ssh-add", "ssh-agent", "sleep", "nginx", "systemctl", "install"} {
		writeDeployFile(t, filepath.Join(fixture.bin, name), deployCommandStub, 0755)
	}
	return fixture
}

func (f deployFixture) run(mode string, extra ...string) (string, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(f.root, "deploy.sh"), mode)
	cmd.Dir = f.root
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "DEPLOY_") && !strings.HasPrefix(item, "DRAMA_LOG_") && !strings.HasPrefix(item, "SSH_") && !strings.HasPrefix(item, "PATH=") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	// 所有场景都不提供 SSH_AUTH_SOCK，避免重新引入对服务器 Agent 的依赖。
	cmd.Env = append(cmd.Env, "PATH="+f.bin+":"+os.Getenv("PATH"), "DEPLOY_TEST_STATE="+f.state, "DEPLOY_TEST_ROOT="+f.root, "DEPLOY_SSH_KEY="+filepath.Join(f.state, "deployment key"), "DEPLOY_KNOWN_HOSTS_FILE="+filepath.Join(f.state, "known hosts"))
	for _, item := range extra {
		if item == "DEPLOY_TEST_MISSING_HOSTS=1" {
			cmd.Env = append(cmd.Env, "DEPLOY_KNOWN_HOSTS_FILE="+filepath.Join(f.state, "missing hosts"))
		} else if item == "DEPLOY_TEST_MISSING_KEY=1" {
			cmd.Env = append(cmd.Env, "DEPLOY_SSH_KEY="+filepath.Join(f.state, "missing key"))
		} else {
			cmd.Env = append(cmd.Env, item)
		}
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (f deployFixture) trace() string {
	f.t.Helper()
	contents, err := os.ReadFile(filepath.Join(f.state, "trace"))
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	return string(contents)
}

func writeDeployFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

const deployCommandStub = `#!/usr/bin/env bash
set -eu
name="$(basename "$0")"
{ printf '%s' "$name"; printf ' <%s>' "$@"; printf '\n'; } >> "$DEPLOY_TEST_STATE/trace"
case "$name:$1" in
  git:pull)
    [ "${DEPLOY_TEST_FAIL:-}" != pull ] || exit 1
    if [ "${DEPLOY_TEST_REPLACE_SCRIPT:-}" = 1 ]; then
      cp "$DEPLOY_TEST_STATE/updated-deploy.sh" "$DEPLOY_TEST_ROOT/deploy.sh"
    fi
    ;;
  git:rev-parse) printf 'abc1234\n' ;;
  ssh-agent:*) exit 90 ;;
  ssh-add:*) exit 91 ;;
  ssh:*) exit 92 ;;
  docker:build)
    [ "${DEPLOY_TEST_FAIL:-}" != build ] || exit 1
    [ -z "${DEPLOY_REEXEC+x}" ]
    [ "$DOCKER_BUILDKIT" = 1 ]
    [ "$2" = --ssh ] && [ "$3" = "default=$DEPLOY_SSH_KEY" ]
    [ -r "$DEPLOY_SSH_KEY" ]
    [ "$4" = --secret ] && [ "$5" = "type=file,id=gitee_known_hosts,src=$DEPLOY_KNOWN_HOSTS_FILE" ]
    [ -r "$DEPLOY_KNOWN_HOSTS_FILE" ]
    ;;
  docker:run)
    if [ "${DEPLOY_TEST_FAIL:-}" = log_prepare ] && [[ " $* " == *" --user root "* ]]; then exit 1; fi
    ;;
  docker:inspect) if [ "${DEPLOY_TEST_FAIL:-}" = startup ]; then printf 'false\n'; else printf 'true\n'; fi ;;
esac
`
