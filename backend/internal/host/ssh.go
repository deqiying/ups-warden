package host

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

const operationTimeout = 15 * time.Second
const outputLimit = 4096

// 动作层已有 deadline 时尊重其配置；直接调用则提供有限的默认超时。
func operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, operationTimeout)
}

// operationConnection 使用 DialContext，并在整个握手/执行期间将取消传递到底层 socket。
func operationConnection(ctx context.Context, h Record) (net.Conn, func(), error) {
	if !validAddress(h.Address) || h.Conn.Port < 1 || h.Conn.Port > 65535 {
		return nil, nil, errors.New("SSH 地址或端口无效")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(h.Address, strconv.Itoa(h.Conn.Port)))
	if err != nil {
		return nil, nil, connectionError(ctx, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return conn, func() { stop(); _ = conn.Close() }, nil
}

func connectionError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var n net.Error
	if errors.As(err, &n) && n.Timeout() {
		return context.DeadlineExceeded
	}
	return errors.New("SSH 连接失败，请检查地址、端口和网络")
}

// FetchHostKey 在收到主机公钥后立即终止握手，不发送任何认证请求。
func FetchHostKey(ctx context.Context, h Record) (string, error) {
	ctx, cancel := operationContext(ctx)
	defer cancel()
	conn, closeConn, err := operationConnection(ctx, h)
	if err != nil {
		return "", err
	}
	defer closeConn()
	var fingerprint string
	stopFetch := errors.New("host key obtained; stop before authentication")
	cfg := &ssh.ClientConfig{HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint = ssh.FingerprintSHA256(key)
		return stopFetch
	}}
	_, _, _, err = ssh.NewClientConn(conn, conn.RemoteAddr().String(), cfg)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if fingerprint != "" {
		return fingerprint, nil
	}
	return "", connectionError(ctx, err)
}

func connect(ctx context.Context, h Record) (*ssh.Client, func(), error) {
	if h.Fingerprint == "" {
		return nil, nil, ErrUnconfirmed
	}
	if !validFingerprint(h.Fingerprint) {
		return nil, nil, ErrFingerprint
	}
	if err := Validate(h); err != nil {
		return nil, nil, err
	}
	var auth ssh.AuthMethod
	if h.Conn.AuthMethod == "password" {
		auth = ssh.Password(h.Credentials.Password.Reveal())
	} else {
		s, err := signer(h.Credentials)
		if err != nil {
			return nil, nil, err
		}
		auth = ssh.PublicKeys(s)
	}
	conn, closeConn, err := operationConnection(ctx, h)
	if err != nil {
		return nil, nil, err
	}
	var trustErr error
	cfg := &ssh.ClientConfig{User: h.Conn.Username, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if subtle.ConstantTimeCompare([]byte(ssh.FingerprintSHA256(key)), []byte(h.Fingerprint)) != 1 {
			trustErr = ErrFingerprint
			return trustErr
		}
		return nil
	}}
	c, chans, reqs, err := ssh.NewClientConn(conn, conn.RemoteAddr().String(), cfg)
	if err != nil {
		closeConn()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if trustErr != nil {
			return nil, nil, trustErr
		}
		return nil, nil, errors.New("SSH 握手或认证失败，请检查凭据与主机状态")
	}
	client := ssh.NewClient(c, chans, reqs)
	return client, func() { _ = client.Close(); closeConn() }, nil
}

// TestConnection 只完成严格指纹检查和认证，不打开命令会话。
func TestConnection(ctx context.Context, h Record) error {
	ctx, cancel := operationContext(ctx)
	defer cancel()
	_, closeConn, err := connect(ctx, h)
	if err != nil {
		return err
	}
	closeConn()
	return nil
}

// TestCommand 固定执行无害 echo，且不继承 sudo，以免把测试变成提权命令。
func TestCommand(ctx context.Context, h Record) (Result, error) {
	h.Conn.Sudo = false
	return Execute(ctx, h, "echo ups-warden-ok", false)
}

// Execute 只有 Start 已获得服务端接受后，关机断连才能记为 sent。
// 超时/取消始终失败；sent 不能替代后续离线确认。
func Execute(ctx context.Context, h Record, command string, shutdown bool) (result Result, err error) {
	start := time.Now()
	result.Status = "failed"
	defer func() { result.DurationMS = time.Since(start).Milliseconds() }()
	ctx, cancel := operationContext(ctx)
	defer cancel()
	if strings.TrimSpace(command) == "" || len(command) > 65536 || strings.ContainsRune(command, 0) {
		return result, errors.New("SSH 命令无效")
	}
	client, closeConn, err := connect(ctx, h)
	if err != nil {
		return result, err
	}
	defer closeConn()
	session, err := client.NewSession()
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, errors.New("SSH 会话创建失败")
	}
	defer session.Close()
	tokens := credentialTokens(h.Credentials)
	out, stderr := newOutput(tokens), newOutput(tokens)
	session.Stdout = out
	session.Stderr = stderr
	defer func() { result.Stdout = out.text(); result.Stderr = stderr.text() }()
	if h.Conn.Sudo {
		// 不代为授予 shell 的 NOPASSWD 权限，目标主机可只放行具体命令。
		command = "sudo -n -- " + command
	}
	if err = session.Start(command); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, errors.New("SSH 命令未被服务端接受")
	}
	err = session.Wait()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err == nil {
		code := 0
		result.ExitCode = &code
		result.Status = "success"
		return result, nil
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitStatus()
		result.ExitCode = &code
		return result, errors.New("SSH 命令退出失败")
	}
	var missing *ssh.ExitMissingError
	if shutdown && (errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.As(err, &missing)) {
		result.Status = "sent"
		return result, nil
	}
	return result, errors.New("SSH 命令未完成或连接中断")
}

func credentialTokens(c Credentials) [][]byte {
	values := []string{c.Password.Reveal(), c.PrivateKey.Reveal(), c.Passphrase.Reveal()}
	// 私钥可能被逐行输出；每行也属于敏感内容。
	values = append(values, strings.Split(c.PrivateKey.Reveal(), "\n")...)
	var tokens [][]byte
	for _, s := range values {
		if s != "" {
			tokens = append(tokens, []byte(s))
		}
	}
	return tokens
}

// 只保留 4KB 加最长凭据的前瞻窗口，先完整识别跨截断点凭据再截断。
// 超出窗口仍返回完整写入长度，避免背压或无限内存增长。
type output struct {
	data     []byte
	tokens   [][]byte
	capacity int
}

func newOutput(tokens [][]byte) *output {
	max := 0
	for _, s := range tokens {
		if len(s) > max {
			max = len(s)
		}
	}
	return &output{tokens: tokens, capacity: outputLimit + max}
}
func (o *output) Write(p []byte) (int, error) {
	n := len(p)
	remain := o.capacity - len(o.data)
	if remain > 0 {
		if len(p) > remain {
			p = p[:remain]
		}
		o.data = append(o.data, p...)
	}
	return n, nil
}
func (o *output) text() string {
	var b strings.Builder
	for i := 0; i < len(o.data) && i < outputLimit; {
		match := 0
		for _, s := range o.tokens {
			if len(s) > match && bytes.HasPrefix(o.data[i:], s) {
				match = len(s)
			}
		}
		if match > 0 {
			b.WriteString("******")
			i += match
		} else {
			b.WriteByte(o.data[i])
			i++
		}
	}
	s := b.String()
	if len(s) > outputLimit {
		s = s[:outputLimit]
	}
	return strings.ToValidUTF8(s, "")
}
