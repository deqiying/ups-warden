package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	controlFile    = "control.json"
	maxControlBody = 64 * 1024
)

// controlInfo 是 control.json 的内容；客户端据此连接守护进程。文件权限 0600，token 不得输出。
type controlInfo struct {
	Schema    int       `json:"schema"`
	PID       int       `json:"pid"`
	Address   string    `json:"address"`
	Token     string    `json:"token"`
	StartedAt time.Time `json:"started_at"`
	Version   string    `json:"version"`
}

// statusView 是 GET /v1/status 的响应。
type statusView struct {
	PID       int       `json:"pid"`
	Address   string    `json:"address"`
	StartedAt time.Time `json:"started_at"`
	Version   string    `json:"version"`
	StateDir  string    `json:"state_dir"`
	Devices   int       `json:"devices"`
	Failed    int       `json:"failed"`
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成控制 token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// controlHandler 构造控制面路由。所有请求都必须携带 Bearer token；浏览器无法读取 token，
// 也不能在简单跨站请求中附加 Authorization，因此本机网页不能调用控制面。
func controlHandler(reg *registry, token string, status func() statusView, shutdown func()) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, status()) })
	mux.HandleFunc("POST /v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]bool{"shutting_down": true})
		shutdown()
	})
	mux.HandleFunc("GET /v1/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"devices": reg.list()})
	})
	mux.HandleFunc("POST /v1/devices", func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if aerr := decodeBody(w, r, &req); aerr != nil {
			writeError(w, aerr)
			return
		}
		view, aerr := reg.create(req)
		if aerr != nil {
			writeError(w, aerr)
			return
		}
		writeJSON(w, http.StatusCreated, view)
	})
	mux.HandleFunc("GET /v1/devices/{name}", func(w http.ResponseWriter, r *http.Request) {
		view, aerr := reg.get(r.PathValue("name"))
		if aerr != nil {
			writeError(w, aerr)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("PATCH /v1/devices/{name}", func(w http.ResponseWriter, r *http.Request) {
		var req patchRequest
		if aerr := decodeBody(w, r, &req); aerr != nil {
			writeError(w, aerr)
			return
		}
		view, aerr := reg.update(r.PathValue("name"), req)
		if aerr != nil {
			writeError(w, aerr)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("DELETE /v1/devices/{name}", func(w http.ResponseWriter, r *http.Request) {
		if aerr := reg.remove(r.PathValue("name")); aerr != nil {
			writeError(w, aerr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"removed": r.PathValue("name")})
	})
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			writeError(w, newAPIError(http.StatusUnauthorized, "unauthorized", "缺少或错误的控制 token"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// decodeBody 限制请求体 64 KiB，拒绝未知字段与尾随内容；PATCH 中出现不可修改字段也按未知字段拒绝。
func decodeBody(w http.ResponseWriter, r *http.Request, out any) *apiError {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return newAPIError(http.StatusRequestEntityTooLarge, "body_too_large", "请求体超过 64 KiB")
		}
		return validationError("请求体无效: %v", err)
	}
	if dec.More() {
		return validationError("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // 客户端断开时写失败无可恢复动作。
}

func writeError(w http.ResponseWriter, e *apiError) {
	writeJSON(w, e.Status, map[string]*apiError{"error": e})
}

// client 是短生命周期的控制面客户端。
type client struct {
	info controlInfo
	http *http.Client
}

// dialControl 读取 control.json 并确认守护进程可达；文件缺失或连接失败都视为未运行。
func dialControl(stateDir string) (*client, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, controlFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNotRunning
	}
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", controlFile, err)
	}
	var info controlInfo
	if err := json.Unmarshal(data, &info); err != nil || info.Schema != 1 || info.Token == "" {
		return nil, fmt.Errorf("%s 内容无效", controlFile)
	}
	// 只连接 loopback，防止被篡改的文件把 token 发往其他主机。
	host, _, err := net.SplitHostPort(info.Address)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("%s 中的控制地址不是 loopback，拒绝连接", controlFile)
	}
	c := &client{info: info, http: &http.Client{Timeout: 10 * time.Second}}
	var st statusView
	if err := c.do(http.MethodGet, "/v1/status", nil, &st); err != nil {
		var aerr *apiError
		if errors.As(err, &aerr) {
			return nil, err
		}
		return nil, errNotRunning
	}
	return c, nil
}

// do 发送请求；守护进程返回的业务错误以 *apiError 返回，其余为连接错误。
func (c *client) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.info.Address+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.info.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连接守护进程: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("读取守护进程响应: %w", err)
	}
	if resp.StatusCode >= 300 {
		var wrapper struct {
			Error *apiError `json:"error"`
		}
		if json.Unmarshal(data, &wrapper) != nil || wrapper.Error == nil {
			return fmt.Errorf("守护进程返回 HTTP %d", resp.StatusCode)
		}
		wrapper.Error.Status = resp.StatusCode
		return wrapper.Error
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("解析守护进程响应: %w", err)
		}
	}
	return nil
}
