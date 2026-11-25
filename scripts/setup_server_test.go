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

// TestSetupServerPreservesConfiguration 验证首次部署与重复初始化均保留已有业务和运维配置。
func TestSetupServerPreservesConfiguration(t *testing.T) {
	for _, mode := range []string{"test", "prod"} {
		t.Run(mode, func(t *testing.T) {
			f := newSetupFixture(t)
			output, err := f.run(mode)
			if err != nil {
				t.Fatalf("初始化失败：%v\n%s", err, output)
			}
			branch := "test"
			if mode == "prod" {
				branch = "main"
			}
			trace := f.read("trace")
			if !strings.Contains(trace, "git <clone> <--branch> <"+branch+">") || !strings.Contains(trace, "deploy <"+mode+"> <"+f.root+"/deployment key> <"+f.root+"/dedicated config/known_hosts>") {
				t.Fatalf("分支或首次部署环境不符：\n%s", trace)
			}
			// 使用独立子进程验证登录环境文件可以恢复包含空格的路径。
			cmd := exec.Command("bash", "--noprofile", "--norc", "-c", `source "$1"; printf '%s\n%s' "$DEPLOY_SSH_KEY" "$DEPLOY_KNOWN_HOSTS_FILE"`, "bash", filepath.Join(f.root, "profile.sh"))
			loaded, err := cmd.CombinedOutput()
			if err != nil || string(loaded) != f.root+"/deployment key\n"+f.root+"/dedicated config/known_hosts" {
				t.Fatalf("环境文件无法正确加载：%v\n%s", err, loaded)
			}
			preserved := map[string]string{}
			for _, name := range []string{"profile.sh", "deployment key", "dedicated config/known_hosts", "project/.git/config", "project/configs/config.test.yaml", "project/configs/config.prod.yaml"} {
				preserved[name] = f.read(name)
			}
			output, err = f.run(mode)
			if err != nil {
				t.Fatalf("重复初始化失败：%v\n%s", err, output)
			}
			for name, before := range preserved {
				if f.read(name) != before {
					t.Errorf("已有配置被修改：%s", name)
				}
			}
			trace = f.read("trace")
			if strings.Count(trace, "git <clone>") != 1 || strings.Count(trace, "<config> <core.sshCommand>") != 1 {
				t.Fatalf("重复初始化不应重新克隆或修改 Git 配置：\n%s", trace)
			}
		})
	}
}

// TestSetupServerStopsOnFailure 验证配置冲突和外部命令失败时停止，不覆盖原文件或报告成功。
func TestSetupServerStopsOnFailure(t *testing.T) {
	for _, failure := range []string{"profile", "repository", "auth", "clone", "deploy"} {
		t.Run(failure, func(t *testing.T) {
			f := newSetupFixture(t)
			if failure == "profile" {
				writeDeployFile(t, filepath.Join(f.root, "profile.sh"), "operator configuration\n", 0600)
			}
			if failure == "repository" {
				writeDeployFile(t, filepath.Join(f.root, "project/.git/config"), "unrelated repository\n", 0600)
			}
			output, err := f.run("test", "DRAMA_SETUP_TEST_FAIL="+failure)
			if err == nil || strings.Contains(output, "初始化和首次部署完成") {
				t.Fatalf("失败场景不应报告成功：%v\n%s", err, output)
			}
			if failure == "profile" && f.read("profile.sh") != "operator configuration\n" {
				t.Fatal("已有环境配置被覆盖")
			}
			if failure == "repository" && f.read("project/.git/config") != "unrelated repository\n" {
				t.Fatal("已有仓库配置被覆盖")
			}
			trace := f.read("trace")
			if failure != "deploy" && strings.Contains(trace, "deploy <") {
				t.Fatalf("准备失败后不应继续部署：\n%s", trace)
			}
			if (failure == "profile" || failure == "repository") && strings.Contains(trace, "<ls-remote>") {
				t.Fatalf("配置冲突时不应继续连接仓库：\n%s", trace)
			}
		})
	}
}

type setupFixture struct {
	t    *testing.T
	root string
}

func newSetupFixture(t *testing.T) setupFixture {
	t.Helper()
	f := setupFixture{t: t, root: filepath.Join(t.TempDir(), "setup with spaces")}
	contents, err := os.ReadFile("setup-server.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeDeployFile(t, filepath.Join(f.root, "setup-server.sh"), string(contents), 0755)
	writeDeployFile(t, filepath.Join(f.root, "deployment key"), "fixture only; not a private key\n", 0600)
	writeDeployFile(t, filepath.Join(f.root, "trace"), "", 0600)
	writeDeployFile(t, filepath.Join(f.root, "bin/git"), setupGitStub, 0755)
	return f
}

func (f setupFixture) run(mode string, extra ...string) (string, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(f.root, "setup-server.sh"), mode, filepath.Join(f.root, "deployment key"), filepath.Join(f.root, "project"))
	cmd.Dir = f.root
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "DRAMA_SETUP_") && !strings.HasPrefix(item, "DEPLOY_") && !strings.HasPrefix(item, "SSH_") && !strings.HasPrefix(item, "PATH=") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	// 不替换 HOME，所有写入通过专用参数定位到临时目录；Git 与部署本身均使用桩。
	cmd.Env = append(cmd.Env, "PATH="+f.root+"/bin:"+os.Getenv("PATH"), "DRAMA_SETUP_TEST_ROOT="+f.root, "DRAMA_SETUP_CONFIG_DIR="+f.root+"/dedicated config", "DRAMA_SETUP_PROFILE_FILE="+f.root+"/profile.sh")
	cmd.Env = append(cmd.Env, extra...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (f setupFixture) read(name string) string {
	f.t.Helper()
	contents, err := os.ReadFile(filepath.Join(f.root, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(contents)
}

const setupGitStub = `#!/usr/bin/env bash
set -eu
{ printf git; printf ' <%s>' "$@"; printf '\n'; } >> "$DRAMA_SETUP_TEST_ROOT/trace"
case "$1" in
  ls-remote)
    [ "${DRAMA_SETUP_TEST_FAIL:-}" != auth ] || exit 31
    if [ ! -e "$DRAMA_SETUP_CONFIG_DIR/known_hosts" ]; then
      printf 'fixture host record\n' > "$DRAMA_SETUP_CONFIG_DIR/known_hosts"
    fi
    ;;
  clone)
    [ "${DRAMA_SETUP_TEST_FAIL:-}" != clone ] || exit 32
    mkdir -p "$5/.git" "$5/configs"
    printf 'original Git config\n' > "$5/.git/config"
    printf 'original test config\n' > "$5/configs/config.test.yaml"
    printf 'original prod config\n' > "$5/configs/config.prod.yaml"
    cat > "$5/deploy.sh" <<'DEPLOY'
#!/usr/bin/env bash
set -eu
printf 'deploy <%s> <%s> <%s>\n' "$1" "$DEPLOY_SSH_KEY" "$DEPLOY_KNOWN_HOSTS_FILE" >> "$DRAMA_SETUP_TEST_ROOT/trace"
[ "${DRAMA_SETUP_TEST_FAIL:-}" != deploy ] || exit 33
DEPLOY
    ;;
  -C)
    case "$3" in
      remote)
        if [ "${DRAMA_SETUP_TEST_FAIL:-}" = repository ]; then
          printf 'git@example.com:unrelated/project.git\n'
        else
          printf 'gitee-drama:tiptops/drama-server.git\n'
        fi
        ;;
      config) printf '%s\n' "$5" >> "$2/.git/config" ;;
    esac
    ;;
esac
`
