package notify

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// 重试时序使用内存 HTTP handler，避免真实 socket 阻止 synctest 的虚拟时间前进。
// 实际 loopback 请求由 transport_test 和 API 集成测试覆盖。
func TestHTTPRetryCountsAndTelegram429(t *testing.T) {
	for _, kind := range []string{"webhook", "telegram"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls []time.Time
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls = append(calls, time.Now())
					if kind == "webhook" {
						w.WriteHeader(503)
						fmt.Fprint(w, "private-response")
						return
					}
					if len(calls) == 1 {
						w.WriteHeader(429)
						fmt.Fprint(w, `{"ok":false,"description":"123:private-token","parameters":{"retry_after":7}}`)
						return
					}
					fmt.Fprint(w, `{"ok":true}`)
				})
				cfg := Config{}
				cred := Credentials{URL: "http://127.0.0.1/in-memory"}
				if kind == "telegram" {
					cfg = Config{APIBase: "http://127.0.0.1", ChatIDs: []string{"123"}}
					cred = Credentials{Token: "123:private-token"}
				}
				deliveries, err := NewDeliveries(kind, cfg, cred)
				if err != nil {
					t.Fatal(err)
				}
				defer deliveries[0].Channel.Close()
				channel := deliveries[0].Channel.(*httpChannel)
				channel.client.Transport = transportRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					return w.Result(), nil
				})
				n, err := sendWithRetry(context.Background(), channel, Message{Body: `{"test":true}`})
				if kind == "webhook" {
					if n != 4 || len(calls) != 4 || err == nil || strings.Contains(err.Error(), "private-response") {
						t.Fatal(n, len(calls), err)
					}
				} else {
					if n != 2 || len(calls) != 2 || err != nil {
						t.Fatal(n, len(calls), err)
					}
					if calls[1].Sub(calls[0]) != 7*time.Second {
						t.Fatal("未遵循 retry_after")
					}
				}
			})
		})
	}
}
func TestSummaryRedactsEscapedCredential(t *testing.T) {
	secret := `sign<&"key`
	text := html.EscapeString(secret)
	summary := SafeSummary("prefix "+text+" suffix", Credentials{SigningKey: secret})
	if strings.Contains(summary, text) {
		t.Fatal("HTML 转义后的凭据进入摘要")
	}
}
