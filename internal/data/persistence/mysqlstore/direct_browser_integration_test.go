package mysqlstore

import (
	"context"
	"drama-server/internal/biz/application"
	"drama-server/internal/biz/consumer"
	"drama-server/internal/biz/directplay"
	"drama-server/internal/biz/homepage"
	"drama-server/internal/biz/subject"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// TestDirectBrowserIntegration 为桌面与移动界面验收提供隔离真实数据库，只在显式开关下监听本机。
func TestDirectBrowserIntegration(t *testing.T) {
	if os.Getenv("TEST_DIRECT_BROWSER") != "1" {
		t.Skip("仅供显式本机直连界面验收")
	}
	f := newIAAFixture(t)
	r, ctx := seedDirect(t, f)
	f.operations, _ = seedRecommendationPublication(t, f)
	f.direct = directplay.NewService(r, nil)
	f.signer = &directHTTPPlayback{}
	submitDirect(t, r, "publish", "")
	home := homepage.NewService(f.store, f.store, f.store)
	if _, err := home.SaveCarousel(homepage.WithManagementContext(ctx), homepage.Carousel{DramaIDs: []string{"201"}, Items: []homepage.CarouselItem{{DramaID: "201", Title: "Long direct carousel title for responsive layout validation", Visible: true}}}); err != nil {
		t.Fatal(err)
	}
	// 第二主体只有自己的直连应用、版本和推荐，不配置 TikTok 发布方凭据。
	other, err := subject.NewService(f.store).Save(t.Context(), "", subject.Input{Name: "隔离验收主体 B"})
	if err != nil {
		t.Fatal(err)
	}
	app := application.Application{SubjectID: other.ID, TikTokAppID: "000103", Name: "主体 B 直连应用", ClientKey: "browser-subject-b", ClientSecret: "fixture-secret", Status: "active", MonetizationMode: "IAP", PlaybackProvider: "byteplus_web", TimeZone: "UTC"}
	if err := f.store.CreateApplication(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	scope := application.ContextForApplication(t.Context(), app)
	bd, err := f.store.FindDrama(scope, "201")
	if err != nil {
		t.Fatal(err)
	}
	bd.Title = "主体 B 独立上线内容"
	bd.TagIDs = []string{"22"}
	if err := f.store.SaveDrama(scope, *bd); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Accept(scope, "201", directplay.Command{RequestID: "browser-subject-b", Kind: "publish"}, "fixture"); err != nil {
		t.Fatal(err)
	}
	for _, save := range []func(context.Context, homepage.Carousel) (homepage.CarouselView, error){home.SaveCarousel, home.SaveHot} {
		if _, err := save(homepage.WithManagementContext(scope), homepage.Carousel{DramaIDs: []string{"201"}, Items: []homepage.CarouselItem{{DramaID: "201", Title: "主体 B 独立推荐", Visible: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	// 官方与直连推荐独立，浏览器可验证两处下线均使用对应权威配置的友好提示。
	if _, err := home.SaveCarousel(homepage.WithManagementContext(application.WithSubject(context.Background(), "1")), homepage.Carousel{DramaIDs: []string{"201"}}); err != nil {
		t.Fatal(err)
	}
	// 内容列表验收包含已上架后的新草稿与另一部草稿；发布快照保持原样。
	draft, err := f.store.FindDrama(ctx, "201")
	if err != nil {
		t.Fatal(err)
	}
	draft.Title = "连载短剧 · 待上线内容"
	if err := f.store.SaveDrama(ctx, *draft); err != nil {
		t.Fatal(err)
	}
	second := consumer.FreezeContent(*draft)
	second.ID = ""
	second.Title = "第二部短剧 · 批量就绪"
	second.Episodes = nil
	if err := f.store.SaveDramaAggregate(ctx, &second, nil); err != nil {
		t.Fatal(err)
	}
	for number := 1; number <= 2; number++ {
		ep := consumer.Episode{DramaID: second.ID, Number: number, Title: fmt.Sprintf("第 %d 集", number), VID: fmt.Sprintf("browser-source-%d", number), MediaStatus: "ready"}
		if err := f.store.SaveEpisode(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}
	handler := f.handler(t)
	mux := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if origin := req.Header.Get("Origin"); origin == "http://127.0.0.1:5174" || origin == "http://localhost:5174" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Application-ID,X-Subject-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		}
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		handler.ServeHTTP(w, req)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("local browser fixture stopped: %v\n", err)
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	fmt.Printf("Direct browser fixture ready: http://127.0.0.1:8080 controller_pid=%d\n", os.Getpid())
	<-signals
}
