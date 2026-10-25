package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ups-warden/internal/app"
	"ups-warden/internal/auth"
	"ups-warden/internal/config"
	"ups-warden/internal/store"
)

const adminPassword = "acceptance-password"

// server 是通过 app.Run 启动的真实服务实例，只使用临时数据目录。
type server struct {
	t      *testing.T
	base   string
	client *http.Client
	csrf   string
}

// startServer 预置管理员后启动服务并登录；测试结束时取消并要求正常退出。
func startServer(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "ups-warden.db"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(db, auth.Options{})
	if err == nil {
		_, err = a.Setup(t.Context(), a.SetupToken(), "admin", adminPassword, "127.0.0.1")
	}
	if err = errors.Join(err, db.Close()); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() { done <- app.Run(ctx, config.Config{Listen: addr, DataDir: dir}, "acceptance", logger, "") }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error("服务关闭失败:", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("服务未在 30 秒内关闭")
		}
	})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{t: t, base: "http://" + addr, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := s.client.Get(s.base + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("服务未启动:", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	s.call("POST", "/auth/login", map[string]any{"username": "admin", "password": adminPassword}, 200, &session)
	s.csrf = session.CSRF
	return s
}

// call 发送 /api/v1 请求并断言状态码；out 非 nil 时解码响应。
func (s *server) call(method, path string, body any, want int, out any) {
	s.t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, s.base+"/api/v1"+path, reader)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", s.csrf)
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	if resp.StatusCode != want {
		s.t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			s.t.Fatal(err)
		}
	}
}

// create 创建资源并返回 id。
func (s *server) create(path string, body any) int64 {
	s.t.Helper()
	var v struct {
		ID int64 `json:"id"`
	}
	s.call("POST", path, body, 201, &v)
	return v.ID
}

type sseEvent struct {
	name     string
	data     []byte
	received time.Time
}

// stream 订阅 SSE，帧按到达顺序写入有界通道；ctx 取消后关闭连接。
// 通道满时丢弃新帧，避免测试读取慢时阻塞服务端写入。
func (s *server) stream(ctx context.Context, buffer int) <-chan sseEvent {
	s.t.Helper()
	req, err := http.NewRequestWithContext(ctx, "GET", s.base+"/api/v1/stream", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	client := &http.Client{Jar: s.client.Jar}
	resp, err := client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		s.t.Fatal("SSE 状态码", resp.StatusCode)
	}
	out := make(chan sseEvent, buffer)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 16<<20)
		var name string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				select {
				case out <- sseEvent{name: name, data: []byte(strings.TrimPrefix(line, "data: ")), received: time.Now()}:
				default:
				}
			}
		}
	}()
	return out
}

// webhook 是本机假 Webhook 接收端，记录到达时间与正文。
type delivery struct {
	body     string
	received time.Time
}

func startWebhook(t *testing.T, buffer int) (string, <-chan delivery) {
	t.Helper()
	ch := make(chan delivery, buffer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case ch <- delivery{body: string(body), received: time.Now()}:
		default:
		}
		w.WriteHeader(204)
	})}
	go srv.Serve(listener)
	t.Cleanup(func() { srv.Close() })
	return "http://" + listener.Addr().String() + "/notify", ch
}

func hostPort(t *testing.T, address string) (string, int) {
	t.Helper()
	host, text, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(text)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}
