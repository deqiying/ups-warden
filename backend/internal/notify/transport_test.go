package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const transportTestToken = "123456:TEST_secret-token"

func transportDeliveries(t *testing.T, kind string, cfg Config, cred Credentials) []Delivery {
	t.Helper()
	deliveries, err := NewDeliveries(kind, cfg, cred)
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range deliveries {
		t.Cleanup(delivery.Channel.Close)
		channel := delivery.Channel.(*httpChannel)
		if channel.client.Timeout != 10*time.Second {
			t.Fatal("single-request timeout must be 10 seconds")
		}
		if cred.ProxyURL == "" && channel.client.Transport.(*http.Transport).Proxy != nil {
			t.Fatal("environment proxy must be disabled")
		}
	}
	return deliveries
}

func assertTransportError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v; want category %q", err, want)
	}
	for _, secret := range []string{transportTestToken, "proxy-password", "header-secret", "signing-secret", "query-secret", "server-secret"} {
		if strings.Contains(fmt.Sprintf("%+v %#v", err, err), secret) {
			t.Fatal("error leaked sensitive data")
		}
	}
	if errors.Unwrap(err) != nil {
		t.Fatal("sensitive original error retained in wrapping chain")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		t.Fatal("raw URL error escaped")
	}
}

func TestTransportWebhookSignatureMethodHeaders(t *testing.T) {
	for _, method := range []string{"", http.MethodPut} {
		t.Run("method_"+method, func(t *testing.T) {
			const body = `{"message":"停电 <raw>"}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantMethod := method
				if wantMethod == "" {
					wantMethod = http.MethodPost
				}
				gotBody, _ := io.ReadAll(r.Body)
				if r.Method != wantMethod || string(gotBody) != body || r.URL.RawQuery != "key=query-secret" {
					t.Errorf("unexpected webhook request: method=%s body=%q", r.Method, gotBody)
				}
				if r.Header.Get("Content-Type") != "application/custom+json" || r.Header.Get("X-Custom") != "header-secret" {
					t.Error("custom headers not applied")
				}
				timestamp := r.Header.Get(timestampHeader)
				seconds, err := strconv.ParseInt(timestamp, 10, 64)
				if err != nil || time.Since(time.Unix(seconds, 0)).Abs() > 5*time.Second {
					t.Error("invalid signing timestamp")
				}
				mac := hmac.New(sha256.New, []byte("signing-secret"))
				_, _ = mac.Write([]byte(timestamp + "." + body))
				if r.Header.Get(signatureHeader) != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
					t.Error("invalid HMAC signature")
				}
				w.WriteHeader(http.StatusCreated)
			}))
			defer server.Close()
			cred := Credentials{URL: server.URL + "/hook?key=query-secret", SigningKey: "signing-secret", Headers: map[string]string{"content-type": "application/custom+json", "X-Custom": "header-secret"}}
			deliveries := transportDeliveries(t, "webhook", Config{Method: method, BodyTemplate: "ignored by transport"}, cred)
			if len(deliveries) != 1 || deliveries[0].Target != "webhook" {
				t.Fatalf("unexpected delivery targets: %+v", deliveries)
			}
			// 工厂必须快照 Header，避免外部修改绕过配置校验。
			cred.Headers["X-Custom"] = "changed"
			if err := deliveries[0].Channel.Send(context.Background(), Message{Body: body}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTransportWebhookStatusAndRedirect(t *testing.T) {
	for _, status := range []int{200, 204, 299, 300, 302, 400, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var followed atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				followed.Add(1)
			}))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Content-Type") != "application/json" || r.Header.Get(signatureHeader) != "" {
					t.Error("invalid unsigned default headers")
				}
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
				if status != 204 {
					_, _ = io.WriteString(w, "server-secret "+transportTestToken)
				}
			}))
			defer server.Close()
			channel := transportDeliveries(t, "webhook", Config{}, Credentials{URL: server.URL})[0].Channel
			err := channel.Send(context.Background(), Message{Body: "{}"})
			if status < 300 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertTransportError(t, err, "HTTP "+strconv.Itoa(status))
			}
			if followed.Load() != 0 || RetryDelay(err) != 0 {
				t.Fatal("redirect followed or webhook delay inferred from body")
			}
		})
	}
}

func TestTransportTelegramTargetsAndFormats(t *testing.T) {
	for _, format := range []string{"text", "html"} {
		t.Run(format, func(t *testing.T) {
			requests := make(chan map[string]string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/bot"+transportTestToken+"/sendMessage" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("invalid Telegram request metadata")
				}
				var payload map[string]string
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				requests <- payload
				_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
			}))
			defer server.Close()
			chats := []string{"-1001234", "@warden_alerts"}
			deliveries := transportDeliveries(t, "telegram", Config{APIBase: server.URL + "/api/", ChatIDs: chats, Format: format}, Credentials{Token: transportTestToken})
			if len(deliveries) != 2 || deliveries[0].Channel == deliveries[1].Channel {
				t.Fatal("expected independent chat deliveries")
			}
			for i, delivery := range deliveries {
				if delivery.Target != chats[i] {
					t.Fatal("incorrect target")
				}
				if err := delivery.Channel.Send(context.Background(), Message{Title: "not separately sent", Body: "<b>停电 &amp; UPS</b>"}); err != nil {
					t.Fatal(err)
				}
				payload := <-requests
				if payload["chat_id"] != chats[i] || payload["text"] != "<b>停电 &amp; UPS</b>" {
					t.Fatal("incorrect target or rendered text changed")
				}
				mode, set := payload["parse_mode"]
				if format == "html" && mode != "HTML" || format == "text" && set {
					t.Fatal("incorrect parse_mode")
				}
				if len(requests) != 0 {
					t.Fatal("single delivery sent to multiple chats")
				}
			}
		})
	}
}

func TestTransportTelegramResponseAndRetryDelay(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
		delay  time.Duration
	}{
		{"success", 200, `{"ok":true}`, "", 0},
		{"empty", 200, "", "invalid telegram response", 0},
		{"missing_ok", 200, `{}`, "invalid telegram response", 0},
		{"null_ok", 200, `{"ok":null}`, "invalid telegram response", 0},
		{"bad_ok", 200, `{"ok":"true"}`, "invalid telegram response", 0},
		{"malformed", 200, "server-secret " + transportTestToken, "invalid telegram response", 0},
		{"trailing_json", 200, `{"ok":true}{}`, "invalid telegram response", 0},
		{"http_200_rejected", 200, `{"ok":false,"description":"` + transportTestToken + `"}`, "telegram rejected", 0},
		{"http_failure_ok", 500, `{"ok":true}`, "HTTP 500", 0},
		{"rate_limit", 429, `{"ok":false,"description":"` + transportTestToken + `","parameters":{"retry_after":17}}`, "HTTP 429", 17 * time.Second},
		{"negative_delay", 429, `{"ok":false,"parameters":{"retry_after":-3}}`, "HTTP 429", 0},
		{"non_429_delay", 400, `{"ok":false,"parameters":{"retry_after":17}}`, "HTTP 400", 0},
		{"overflow_safe", 429, `{"ok":false,"parameters":{"retry_after":9223372036854775807}}`, "HTTP 429", time.Duration((1<<63-1)/int64(time.Second)) * time.Second},
		{"oversize", 200, `{"ok":true,"description":"` + strings.Repeat("x", maxTransportBody) + `"}`, "response too large", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			channel := transportDeliveries(t, "telegram", Config{APIBase: server.URL, ChatIDs: []string{"123"}}, Credentials{Token: transportTestToken})[0].Channel
			err := channel.Send(context.Background(), Message{Body: "test"})
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertTransportError(t, err, tc.want)
			}
			if RetryDelay(err) != tc.delay || err != nil && RetryDelay(fmt.Errorf("service: %w", err)) != tc.delay {
				t.Fatalf("unexpected retry delay: %s", RetryDelay(err))
			}
		})
	}
	if RetryDelay(errors.New("retry_after=100")) != 0 || RetryDelay(nil) != 0 {
		t.Fatal("unexpected delay from unrelated error")
	}
}

func TestTransportTelegramProxyAndRedirect(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Scheme != "http" || r.URL.Host != "telegram.invalid" || r.URL.Path != "/bot"+transportTestToken+"/sendMessage" {
			t.Error("request did not use configured forward proxy")
		}
		if r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwcm94eS1wYXNzd29yZA==" {
			t.Error("proxy credentials not sent to proxy")
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("user", "proxy-password")
	channel := transportDeliveries(t, "telegram", Config{APIBase: "http://telegram.invalid", ChatIDs: []string{"123"}}, Credentials{Token: transportTestToken, ProxyURL: proxyURL.String()})[0].Channel
	if err := channel.Send(context.Background(), Message{Body: "via proxy"}); err != nil || requests.Load() != 1 {
		t.Fatalf("proxy send failed: %v", err)
	}

	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer redirect.Close()
	channel = transportDeliveries(t, "telegram", Config{APIBase: redirect.URL, ChatIDs: []string{"123"}}, Credentials{Token: transportTestToken})[0].Channel
	assertTransportError(t, channel.Send(context.Background(), Message{}), "HTTP 307")
	if followed.Load() != 0 {
		t.Fatal("Telegram redirect followed")
	}
}

type transportRoundTripFunc func(*http.Request) (*http.Response, error)

func (f transportRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type transportTrackedBody struct {
	io.Reader
	closed bool
}

func (b *transportTrackedBody) Close() error { b.closed = true; return nil }

func TestTransportCancellationTimeoutAndSafeErrors(t *testing.T) {
	for _, kind := range []string{"webhook", "telegram"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "telegram" {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			cfg, cred := Config{}, Credentials{URL: server.URL + "?key=query-secret"}
			if kind == "telegram" {
				cfg, cred = Config{APIBase: server.URL, ChatIDs: []string{"123"}}, Credentials{Token: transportTestToken}
			}
			channel := transportDeliveries(t, kind, cfg, cred)[0].Channel.(*httpChannel)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			assertTransportError(t, channel.Send(ctx, Message{}), "request canceled")
			ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			assertTransportError(t, channel.Send(ctx, Message{}), "request timed out")
			channel.client.Timeout = 25 * time.Millisecond
			assertTransportError(t, channel.Send(context.Background(), Message{}), "request timed out")

			channel.client.Transport = transportRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				remaining := time.Until(func() time.Time { d, _ := r.Context().Deadline(); return d }())
				if remaining <= 0 || remaining > requestTimeout {
					t.Error("missing bounded request deadline")
				}
				return nil, &url.Error{Op: "Post", URL: "http://user:proxy-password@host/bot" + transportTestToken, Err: errors.New("header-secret server-secret")}
			})
			assertTransportError(t, channel.Send(context.Background(), Message{}), "transport failed")
		})
	}
}

func TestTransportResponseBodyBoundAndClose(t *testing.T) {
	for _, kind := range []string{"webhook", "telegram"} {
		for _, oversized := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/oversized=%t", kind, oversized), func(t *testing.T) {
				cfg, cred := Config{}, Credentials{URL: "http://127.0.0.1"}
				if kind == "telegram" {
					cfg, cred = Config{APIBase: "http://127.0.0.1", ChatIDs: []string{"123"}}, Credentials{Token: transportTestToken}
				}
				channel := transportDeliveries(t, kind, cfg, cred)[0].Channel.(*httpChannel)
				content := `{"ok":true}` + strings.Repeat(" ", maxTransportBody-len(`{"ok":true}`))
				if oversized {
					content += " "
				}
				body := &transportTrackedBody{Reader: strings.NewReader(content)}
				channel.client.Transport = transportRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
				})
				err := channel.Send(context.Background(), Message{})
				if oversized && kind == "telegram" {
					assertTransportError(t, err, "response too large")
				} else if err != nil {
					t.Fatal(err)
				}
				if !body.closed {
					t.Fatal("response body not closed")
				}
			})
		}
	}
}

func TestTransportConfigDefaultsAndValidation(t *testing.T) {
	web, telegram := Config{}, Config{}
	NormalizeConfig("webhook", &web)
	NormalizeConfig("telegram", &telegram)
	NormalizeConfig("webhook", nil)
	if web.Method != "POST" || web.Format != "json" || telegram.APIBase != "https://api.telegram.org" || telegram.Format != "text" {
		t.Fatal("incorrect defaults")
	}
	type validationCase struct {
		name string
		kind string
		cfg  Config
		cred Credentials
		ok   bool
	}
	cases := []validationCase{
		{"webhook_defaults", "webhook", Config{}, Credentials{URL: "https://example.invalid/hook?key=query-secret"}, true},
		{"webhook_template_boundary", "webhook", Config{BodyTemplate: strings.Repeat("a", maxTransportBody)}, Credentials{URL: "http://example.invalid"}, true},
		{"webhook_missing_url", "webhook", Config{}, Credentials{}, false},
		{"webhook_wrong_method", "webhook", Config{Method: "GET"}, Credentials{URL: "http://example.invalid"}, false},
		{"webhook_wrong_format", "webhook", Config{Format: "html"}, Credentials{URL: "http://example.invalid"}, false},
		{"webhook_template_limit", "webhook", Config{BodyTemplate: strings.Repeat("a", maxTransportBody+1)}, Credentials{URL: "http://example.invalid"}, false},
		{"unknown_kind", "unknown", Config{}, Credentials{}, false},
		{"telegram_defaults", "telegram", Config{ChatIDs: []string{"1", "-100123", "@alert_channel"}}, Credentials{Token: transportTestToken}, true},
		{"telegram_html", "telegram", Config{ChatIDs: []string{"1"}, Format: "html", APIBase: "https://example.invalid/custom/"}, Credentials{Token: transportTestToken, ProxyURL: "https://user:proxy-password@proxy.invalid:8443"}, true},
		{"telegram_no_chat", "telegram", Config{}, Credentials{Token: transportTestToken}, false},
		{"telegram_many_chats", "telegram", Config{ChatIDs: make([]string, 33)}, Credentials{Token: transportTestToken}, false},
		{"telegram_duplicate", "telegram", Config{ChatIDs: []string{"1", "1"}}, Credentials{Token: transportTestToken}, false},
		{"telegram_duplicate_username", "telegram", Config{ChatIDs: []string{"@Alerts", "@alerts"}}, Credentials{Token: transportTestToken}, false},
		{"telegram_no_token", "telegram", Config{ChatIDs: []string{"1"}}, Credentials{}, false},
		{"telegram_bad_format", "telegram", Config{ChatIDs: []string{"1"}, Format: "json"}, Credentials{Token: transportTestToken}, false},
	}
	for _, badURL := range []string{"ftp://example.invalid", "http://user:server-secret@example.invalid", "http://", "http://example.invalid/#frag", "http://example.invalid/#", "http://example.invalid/\nserver-secret", strings.Repeat("x", 8193)} {
		cases = append(cases, validationCase{"webhook_bad_url", "webhook", Config{}, Credentials{URL: badURL}, false})
	}
	for _, base := range []string{"ftp://example.invalid", "http://user:server-secret@example.invalid", "http://example.invalid/?q=query-secret", "http://example.invalid/?", "http://example.invalid/#fragment"} {
		cases = append(cases, validationCase{"telegram_bad_base", "telegram", Config{APIBase: base, ChatIDs: []string{"1"}}, Credentials{Token: transportTestToken}, false})
	}
	for _, proxy := range []string{"socks5://proxy.invalid", "http://proxy.invalid/?q=secret", "http://proxy.invalid/#fragment", "http://proxy.invalid/path"} {
		cases = append(cases, validationCase{"telegram_bad_proxy", "telegram", Config{ChatIDs: []string{"1"}}, Credentials{Token: transportTestToken, ProxyURL: proxy}, false})
	}
	for _, token := range []string{"../token", "token/path", "token?x", "token#x", "token%2f", "token\r\nX-Evil: x", "token space", strings.Repeat("a", 1025)} {
		cases = append(cases, validationCase{"telegram_bad_token", "telegram", Config{ChatIDs: []string{"1"}}, Credentials{Token: token}, false})
	}
	for _, chat := range []string{"", " ", "0", "-0", "+1", "01", "1/2", "1\n", "9223372036854775808", "@bad!name", "@1name", "@abcd"} {
		cases = append(cases, validationCase{"telegram_bad_chat", "telegram", Config{ChatIDs: []string{chat}}, Credentials{Token: transportTestToken}, false})
	}
	for _, header := range []map[string]string{
		{"Bad Name": "secret"}, {"X\r\nBad": "secret"}, {"": "secret"}, {"X-Test": "secret\r\nInjected: yes"}, {"X-Test": "bad\x00"},
		{"Host": "secret"}, {"content-length": "0"}, {"TRANSFER-ENCODING": "chunked"}, {strings.ToLower(signatureHeader): "secret"}, {timestampHeader: "secret"},
		{"X-Test": "a", "x-test": "b"}, {"X-Test": strings.Repeat("a", 8193)},
	} {
		cases = append(cases, validationCase{"webhook_bad_header", "webhook", Config{}, Credentials{URL: "http://example.invalid", Headers: header}, false})
	}
	for _, cfg := range []Config{{APIBase: "http://example.invalid"}, {ChatIDs: []string{"1"}}} {
		cases = append(cases, validationCase{"webhook_foreign_config", "webhook", cfg, Credentials{URL: "http://example.invalid"}, false})
	}
	for _, cred := range []Credentials{{URL: "http://example.invalid", Token: transportTestToken}, {URL: "http://example.invalid", ProxyURL: "http://proxy.invalid"}, {URL: "http://example.invalid", SigningKey: strings.Repeat("x", 4097)}} {
		cases = append(cases, validationCase{"webhook_bad_credential", "webhook", Config{}, cred, false})
	}
	for _, cfg := range []Config{{ChatIDs: []string{"1"}, Method: "POST"}, {ChatIDs: []string{"1"}, BodyTemplate: "template"}} {
		cases = append(cases, validationCase{"telegram_foreign_config", "telegram", cfg, Credentials{Token: transportTestToken}, false})
	}
	for _, cred := range []Credentials{{Token: transportTestToken, URL: "http://example.invalid"}, {Token: transportTestToken, Headers: map[string]string{"X-Test": "secret"}}, {Token: transportTestToken, SigningKey: "secret"}} {
		cases = append(cases, validationCase{"telegram_foreign_credential", "telegram", Config{ChatIDs: []string{"1"}}, cred, false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(tc.kind, tc.cfg, tc.cred)
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			assertTransportError(t, err, "")
			if deliveries, createErr := NewDeliveries(tc.kind, tc.cfg, tc.cred); createErr == nil || len(deliveries) != 0 {
				t.Fatal("factory did not validate before construction")
			}
		})
	}
}

func TestTransportTelegramHTTPSProxy(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Host != "telegram.invalid" || r.TLS == nil {
			t.Error("expected TLS connection to explicit proxy")
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer proxy.Close()
	channel := transportDeliveries(t, "telegram", Config{APIBase: "http://telegram.invalid", ChatIDs: []string{"123"}}, Credentials{Token: transportTestToken, ProxyURL: proxy.URL})[0].Channel.(*httpChannel)
	// 只信任 httptest 的本机证书，不禁用 TLS 校验。
	channel.client.Transport.(*http.Transport).TLSClientConfig = proxy.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if err := channel.Send(context.Background(), Message{}); err != nil || requests.Load() != 1 {
		t.Fatalf("HTTPS proxy failed: %v", err)
	}
}

func TestTransportHeaderAndChatLimits(t *testing.T) {
	cred := Credentials{URL: "http://example.invalid", Headers: make(map[string]string)}
	for i := range 65 {
		cred.Headers[fmt.Sprintf("X-Header-%d", i)] = "value"
	}
	assertTransportError(t, ValidateConfig("webhook", Config{}, cred), "invalid webhook headers")
	cred.Headers = map[string]string{strings.Repeat("X", 257): "value"}
	assertTransportError(t, ValidateConfig("webhook", Config{}, cred), "invalid webhook headers")
	cred.Headers = make(map[string]string)
	for i := range 9 {
		cred.Headers[fmt.Sprintf("X-Header-%d", i)] = strings.Repeat("x", 8192)
	}
	assertTransportError(t, ValidateConfig("webhook", Config{}, cred), "invalid webhook headers")
	chats := make([]string, 32)
	for i := range chats {
		chats[i] = strconv.Itoa(i + 1)
	}
	if err := ValidateConfig("telegram", Config{ChatIDs: chats}, Credentials{Token: transportTestToken}); err != nil {
		t.Fatal(err)
	}
}

type transportFailedReader struct{}

func (transportFailedReader) Read([]byte) (int, error) {
	return 0, errors.New("server-secret " + transportTestToken)
}

func TestTransportBodyReadErrorAndClose(t *testing.T) {
	for _, kind := range []string{"webhook", "telegram"} {
		cfg, cred := Config{}, Credentials{URL: "http://127.0.0.1"}
		if kind == "telegram" {
			cfg, cred = Config{APIBase: "http://127.0.0.1", ChatIDs: []string{"123"}}, Credentials{Token: transportTestToken}
		}
		channel := transportDeliveries(t, kind, cfg, cred)[0].Channel.(*httpChannel)
		body := &transportTrackedBody{Reader: transportFailedReader{}}
		channel.client.Transport = transportRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
		})
		if err := channel.Send(context.Background(), Message{}); kind == "telegram" {
			assertTransportError(t, err, "transport failed")
		} else if err != nil {
			t.Fatal("Webhook 2xx 不应因响应体失败重试", err)
		}
		if !body.closed {
			t.Fatal("failed response body not closed")
		}
	}
}
