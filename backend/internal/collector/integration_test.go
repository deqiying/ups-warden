package collector

import (
	"context"
	"net"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
	"ups-warden/internal/profile"
	"ups-warden/internal/secret"
	"ups-warden/internal/sim"
)

// 真实驱动只连接 sim.Start 创建的 loopback 随机端口，绝不读取本地用户配置。
func TestLoopbackProbeThreeProtocols(t *testing.T) {
	server, err := sim.Start(sim.Config{ET1K: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	}()
	sa, ma, aa := server.Addresses()
	s := New(nil, nil, nil, silentOptions())
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	for i, key := range []string{"et1k-snmp", "et1k-modbus", "et1k-ayi9-json"} {
		t.Run(key, func(t *testing.T) {
			p, err := profile.Builtin(key)
			if err != nil {
				t.Fatal(err)
			}
			host, portText, err := net.SplitHostPort([]string{sa, ma, aa}[i])
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				t.Fatal("sim target must be loopback")
			}
			credential := secret.String{}
			if p.Protocol == driver.ProtocolSNMP {
				credential = secret.NewString("sim-public")
			}
			result, err := s.Test(context.Background(), Source{ID: int64(i + 1), Protocol: p.Protocol, Host: host, Port: port, Profile: p, Credential: credential})
			if err != nil {
				t.Fatal(err)
			}
			if !observation(result.Snapshot) || len(result.Trace) == 0 || result.Raw.Protocol != p.Protocol || len(result.Recommendations) == 0 {
				t.Fatalf("incomplete result %#v", result)
			}
			if _, _, ok := s.Diagnostics(int64(i + 1)); ok {
				t.Fatal("temporary probe polluted cache")
			}
		})
	}
}

func TestCustomProfileAndBuiltinPrecedence(t *testing.T) {
	db := newDB(t)
	seed(t, db)
	content := `schema: 1
key: custom-test
name: 自定义测试
version: 3
protocol: snmp
fields:
  - {field: battery_voltage, source: {oid: 1.3.6.1.2.1.33.1.2.5.0}, type: int, scale: 0.1}
`
	execSQL(t, db, `INSERT INTO profiles(key,name,protocol,version,content,created_at,updated_at) VALUES('custom-test','test','snmp',3,?,1,1)`, content)
	execSQL(t, db, `INSERT INTO profiles(key,name,protocol,version,content,created_at,updated_at) VALUES('rfc1628-generic','shadow','snmp',99,'invalid',1,1)`)
	s := New(db, nil, nil, silentOptions())
	defer s.Close()
	sources, err := s.load(context.Background())
	if err != nil || sources[0].Profile.Version != 1 {
		t.Fatalf("builtin shadowed %v", err)
	}
	execSQL(t, db, `UPDATE sources SET profile_key='custom-test',profile_version=3 WHERE id=1`)
	sources, err = s.load(context.Background())
	if err != nil || sources[0].Profile.Key != "custom-test" {
		t.Fatalf("custom load %v", err)
	}
	profiles, err := s.recommendationProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range profiles {
		if p.Key == "custom-test" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom recommendation missing")
	}
	execSQL(t, db, `UPDATE profiles SET version=4 WHERE key='custom-test'`)
	if _, err := s.load(context.Background()); err == nil {
		t.Fatal("content/row version mismatch accepted")
	}
}

func TestInjectedGoCancellationAndSinkWait(t *testing.T) {
	source := testSource(t)
	synctest.Test(t, func(t *testing.T) {
		appCtx, appCancel := context.WithCancel(context.Background())
		defer appCancel()
		called := 0
		opts := silentOptions()
		opts.Go = func(name string, fn func(context.Context)) {
			if name != "collector-1" {
				t.Error("task name")
			}
			called++
			go fn(appCtx)
		}
		entered := make(chan struct{})
		release := make(chan struct{})
		s := New(nil, nil, func(ctx context.Context, _ model.SourceResult) error {
			close(entered)
			<-ctx.Done()
			<-release
			return ctx.Err()
		}, opts)
		startMemory(s, source, &fakeDriver{poll: func(context.Context) (driver.RawData, error) { return goodRaw(), nil }})
		<-entered
		if called != 1 {
			t.Fatal("Go ignored")
		}
		appCancel()
		synctest.Wait()
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close did not wait for sink")
		default:
		}
		close(release)
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	})
}

func TestPreparationAtomicity(t *testing.T) {
	db := newDB(t)
	seed(t, db)
	execSQL(t, db, `INSERT INTO devices(id,name) VALUES(2,'second')`)
	execSQL(t, db, `INSERT INTO sources(id,device_id,protocol,host,port,profile_key,profile_version,poll_ms,timeout_ms) VALUES(2,2,'snmp','127.0.0.2',161,'rfc1628-generic',1,10000,2000)`)
	d := &fakeDriver{poll: func(context.Context) (driver.RawData, error) {
		t.Error("poll before full validation")
		return goodRaw(), nil
	}}
	opts := silentOptions()
	opts.Factory = func(source Source, _ driver.ReadPlan) (driver.Driver, error) {
		if source.ID == 2 {
			return nil, ErrConfig
		}
		return d, nil
	}
	s := New(db, nil, func(context.Context, model.SourceResult) error { return nil }, opts)
	defer s.Close()
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("invalid factory accepted")
	}
	if d.closed.Load() != 1 {
		t.Fatal("prepared driver leaked")
	}
	if _, ok := s.Status(1); ok {
		t.Fatal("partial start")
	}
}

func TestBudgetBoundary(t *testing.T) {
	for _, tc := range []struct {
		poll, timeout time.Duration
		retries       int
		want          time.Duration
	}{
		{200 * time.Millisecond, time.Second, 10, 180 * time.Millisecond},
		{time.Hour, time.Minute, 10, 11 * time.Minute},
		{time.Second, time.Millisecond, 0, time.Millisecond},
	} {
		source := testSource(t)
		source.PollInterval, source.Timeout, source.Retries = tc.poll, tc.timeout, tc.retries
		if _, err := validate(source); err != nil {
			t.Fatal(err)
		}
		if got := pollBudget(source); got != tc.want {
			t.Fatalf("budget=%v want=%v", got, tc.want)
		}
	}
}
