package host

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

type sshFake struct {
	h        Record
	auth     atomic.Int32
	commands chan string
	key      ed25519.PrivateKey
}

func newSSHFake(t *testing.T) *sshFake {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &sshFake{commands: make(chan string, 32), key: key}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	f.h = Record{Name: "loopback", Address: "127.0.0.1", Kind: "linux", Conn: Connection{Port: p, Username: "tester", AuthMethod: "password"}, Credentials: Credentials{Password: secret.NewString("test-only-password")}, Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), OnlineCheck: OnlineCheck{Type: "tcp", Port: p}}
	cfg := &ssh.ServerConfig{
		AuthLogCallback: func(_ ssh.ConnMetadata, _ string, _ error) { f.auth.Add(1) },
		PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if string(p) != "test-only-password" {
				return nil, errors.New("bad password")
			}
			return nil, nil
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(k.Marshal(), signer.PublicKey().Marshal()) {
				return nil, errors.New("bad key")
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(signer)
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	closing := false
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closing {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			connections[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				s, chans, requests, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer s.Close()
				go ssh.DiscardRequests(requests)
				for newChannel := range chans {
					if newChannel.ChannelType() != "session" {
						_ = newChannel.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					ch, reqs, err := newChannel.Accept()
					if err != nil {
						return
					}
					for req := range reqs {
						if req.Type != "exec" {
							_ = req.Reply(false, nil)
							continue
						}
						var payload struct{ Command string }
						if ssh.Unmarshal(req.Payload, &payload) != nil {
							_ = req.Reply(false, nil)
							continue
						}
						f.commands <- payload.Command
						if payload.Command == "drop-before-accept" {
							_ = s.Close()
							break
						}
						if payload.Command == "reject" {
							_ = req.Reply(false, nil)
							_ = ch.Close()
							break
						}
						_ = req.Reply(true, nil)
						switch payload.Command {
						case "disconnect":
							_ = s.Close()
						case "hang":
							_, _ = io.Copy(io.Discard, ch)
						default:
							if payload.Command == "spam" {
								_, _ = io.WriteString(ch, strings.Repeat("a", 4090)+"test-only-password"+strings.Repeat("b", 6000))
								_, _ = io.WriteString(ch.Stderr(), "test-only-password"+strings.Repeat("c", 6000))
							} else {
								_, _ = io.WriteString(ch, "ups-warden-ok\n")
							}
							code := uint32(0)
							if payload.Command == "nonzero" {
								code = 7
							}
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{code}))
							_ = ch.Close()
						}
						break
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		closing = true
		for c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return f
}
func takeCommand(t *testing.T, f *sshFake) string {
	t.Helper()
	select {
	case command := <-f.commands:
		return command
	case <-time.After(2 * time.Second):
		t.Fatal("命令未到达 fake SSH")
		return ""
	}
}
func TestFingerprintFetchNeverAuthenticatesAndStrictTrust(t *testing.T) {
	f := newSSHFake(t)
	fp, err := FetchHostKey(t.Context(), f.h)
	if err != nil || fp != f.h.Fingerprint {
		t.Fatal(fp, err)
	}
	// 回调在 NewServerConn 的认证阶段之前终止，AuthLogCallback 包括 none 也必须为零。
	if f.auth.Load() != 0 {
		t.Fatal("fetch 发送了认证请求")
	}
	h := f.h
	h.Fingerprint = ""
	if _, err := Execute(t.Context(), h, "never", false); !errors.Is(err, ErrUnconfirmed) {
		t.Fatal(err)
	}
	h.Fingerprint = "SHA256:" + strings.Repeat("A", 43)
	if _, err := Execute(t.Context(), h, "never", false); !errors.Is(err, ErrFingerprint) {
		t.Fatal(err)
	}
	if f.auth.Load() != 0 || len(f.commands) != 0 {
		t.Fatal("信任失败仍执行认证或命令")
	}
}
func TestPasswordPrivateKeyAndFixedTestCommand(t *testing.T) {
	f := newSSHFake(t)
	if err := TestConnection(t.Context(), f.h); err != nil {
		t.Fatal(err)
	}
	if len(f.commands) != 0 {
		t.Fatal("测试连接执行了命令")
	}
	for _, encrypted := range []bool{false, true} {
		h := f.h
		h.Conn.AuthMethod = "private_key"
		h.Credentials = Credentials{}
		var block *pem.Block
		var err error
		if encrypted {
			block, err = ssh.MarshalPrivateKeyWithPassphrase(f.key, "test", []byte("test-only-passphrase"))
			h.Credentials.Passphrase = secret.NewString("test-only-passphrase")
		} else {
			block, err = ssh.MarshalPrivateKey(f.key, "test")
		}
		if err != nil {
			t.Fatal(err)
		}
		h.Credentials.PrivateKey = secret.NewString(string(pem.EncodeToMemory(block)))
		h.Conn.Sudo = true
		result, err := TestCommand(t.Context(), h)
		if err != nil || result.Status != "success" || result.ExitCode == nil || *result.ExitCode != 0 || result.Stdout != "ups-warden-ok\n" {
			t.Fatal(result, err)
		}
		if command := takeCommand(t, f); command != "echo ups-warden-ok" {
			t.Fatal(command)
		}
	}
	h := f.h
	h.Conn.Sudo = true
	if _, err := Execute(t.Context(), h, "echo 'safe'", false); err != nil {
		t.Fatal(err)
	}
	if command := takeCommand(t, f); command != "sudo -n -- echo 'safe'" || strings.Contains(command, "test-only-password") {
		t.Fatal(command)
	}
}
func TestShutdownOnlySentAfterAcceptedAndNormalDisconnectFails(t *testing.T) {
	f := newSSHFake(t)
	for _, test := range []struct {
		command  string
		shutdown bool
		status   string
		fails    bool
	}{
		{"disconnect", true, "sent", false}, {"disconnect", false, "failed", true}, {"reject", true, "failed", true}, {"drop-before-accept", true, "failed", true}, {"nonzero", true, "failed", true},
	} {
		result, err := Execute(t.Context(), f.h, test.command, test.shutdown)
		if result.Status != test.status || (err != nil) != test.fails {
			t.Fatalf("%+v: %+v %v", test, result, err)
		}
		if test.command == "nonzero" && (result.ExitCode == nil || *result.ExitCode != 7) {
			t.Fatal(result)
		}
		if takeCommand(t, f) != test.command {
			t.Fatal("unexpected command")
		}
	}
}
func TestCancellationClosesConnectionAndNeverSent(t *testing.T) {
	f := newSSHFake(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		r, err := Execute(ctx, f.h, "hang", true)
		if r.Status == "sent" {
			err = errors.New("cancelled shutdown became sent")
		}
		done <- err
	}()
	if takeCommand(t, f) != "hang" {
		t.Fatal("wrong command")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消未关闭底层连接")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan struct{})
	go func() {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(io.Discard, c)
		close(received)
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	h := f.h
	h.Conn.Port, _ = strconv.Atoi(port)
	deadline, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer stop()
	if _, err := FetchHostKey(deadline, h); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("握手超时未关闭连接")
	}
}
func TestOutputBoundedAndRedactedBeforeTruncation(t *testing.T) {
	f := newSSHFake(t)
	result, err := Execute(t.Context(), f.h, "spam", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) > 4096 || len(result.Stderr) > 4096 || strings.Contains(result.Stdout, "test-") || strings.Contains(result.Stderr, "test-") {
		t.Fatal("输出未限长或泄密")
	}
	if !strings.HasSuffix(result.Stdout, "******") || !strings.HasPrefix(result.Stderr, "******") {
		t.Fatal("跨截断点凭据未脱敏")
	}
	out := newOutput(credentialTokens(Credentials{PrivateKey: secret.NewString("-----BEGIN KEY-----\nprivate-key-line\n-----END KEY-----"), Passphrase: secret.NewString("phrase")}))
	_, _ = out.Write([]byte("private-key-"))
	_, _ = out.Write([]byte("line phrase"))
	if strings.Contains(out.text(), "private") || strings.Contains(out.text(), "phrase") {
		t.Fatal(out.text())
	}
}
func TestTCPOnlineAndEncryptedPersistence(t *testing.T) {
	f := newSSHFake(t)
	if ok, err := CheckOnline(t.Context(), f.h); !ok || err != nil {
		t.Fatal(ok, err)
	}
	h := f.h
	h.OnlineCheck.Port = 0
	if _, err := CheckOnline(t.Context(), h); err == nil {
		t.Fatal("bad port accepted")
	}
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "host.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cipher, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	conn, encrypted, online, err := Encode(f.h, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("test-only-password")) {
		t.Fatal("plaintext persisted")
	}
	if _, err := cipher.Decrypt(encrypted, "channels.secret"); err == nil {
		t.Fatal("AAD 未绑定")
	}
	_, err = db.Write().Exec("INSERT INTO hosts(name,address,kind,conn,secret,hostkey_fingerprint,online_check) VALUES(?,?,?,?,?,?,?)", f.h.Name, f.h.Address, f.h.Kind, conn, encrypted, f.h.Fingerprint, online)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(t.Context(), db, cipher, 1)
	if err != nil || got.Credentials.Password.Reveal() != "test-only-password" {
		t.Fatal(err)
	}
	if err := Validate(got); err != nil {
		t.Fatal(err)
	}
}
func TestICMPCodecAndPermissionHint(t *testing.T) {
	request, err := echoRequest()
	if err != nil {
		t.Fatal(err)
	}
	response := bytes.Clone(request)
	response[0] = 0
	if !echoReply(response, request) {
		t.Fatal("valid echo rejected")
	}
	response[8] ^= 1
	if echoReply(response, request) {
		t.Fatal("unrelated echo accepted")
	}
	if echoReply([]byte{0, 0}, request) {
		t.Fatal("short packet accepted")
	}
	if hint := icmpPermission(&net.OpError{Op: "listen", Err: os.ErrPermission}).Error(); !strings.Contains(hint, "权限不足") || !strings.Contains(hint, "TCP") {
		t.Fatal(hint)
	}
}
