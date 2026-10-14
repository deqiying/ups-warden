package collector

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
	"ups-warden/internal/normalize"
	"ups-warden/internal/profile"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

type fakeDriver struct {
	poll   func(context.Context) (driver.RawData, error)
	probe  func(context.Context) (driver.DeviceInfo, driver.RawData, error)
	closed atomic.Int32
}

func (d *fakeDriver) Poll(ctx context.Context) (driver.RawData, error) { return d.poll(ctx) }
func (d *fakeDriver) Probe(ctx context.Context) (driver.DeviceInfo, driver.RawData, error) {
	if d.probe != nil {
		return d.probe(ctx)
	}
	raw, err := d.Poll(ctx)
	return driver.DeviceInfo{Model: "mock"}, raw, err
}
func (d *fakeDriver) Close() error { d.closed.Add(1); return nil }
func testSource(t *testing.T) Source {
	t.Helper()
	p, err := profile.Builtin("rfc1628-generic")
	if err != nil {
		t.Fatal(err)
	}
	return Source{ID: 1, DeviceID: 1, Protocol: driver.ProtocolSNMP, Host: "127.0.0.1", Port: 161, Profile: p, PollInterval: 10 * time.Second, Timeout: 2 * time.Second}
}
func goodRaw() driver.RawData {
	return driver.RawData{Protocol: driver.ProtocolSNMP, SNMP: map[string]driver.SNMPValue{"1.3.6.1.2.1.33.1.2.5.0": {Tag: 2, Int: 240}}, Errors: []driver.BlockError{{OID: "1.3.6.1.2.1.33.1.2.4.0", Kind: "decode_error"}}}
}
func silentOptions() Options {
	return Options{Jitter: func(time.Duration) time.Duration { return 0 }, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
}
func startMemory(s *Supervisor, source Source, d *fakeDriver) {
	s.runCtx = context.Background()
	s.started = true
	s.launch(&worker{source: source, driver: d, done: make(chan struct{}), gate: make(chan struct{}, 1)})
}
func newDB(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "collector.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
func execSQL(t *testing.T, db *store.Store, query string, args ...any) {
	t.Helper()
	if _, err := db.Write().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}
func seed(t *testing.T, db *store.Store) {
	t.Helper()
	execSQL(t, db, `INSERT INTO devices(id,name) VALUES(1,'test')`)
	execSQL(t, db, `INSERT INTO sources(id,device_id,protocol,host,port,profile_key,profile_version,poll_ms,timeout_ms) VALUES(1,1,'snmp','127.0.0.1',161,'rfc1628-generic',1,10000,2000)`)
}

func TestPollingJitterBackoffRecovery(t *testing.T) {
	source := testSource(t)
	synctest.Test(t, func(t *testing.T) {
		var times []time.Time
		var results []model.SourceResult
		var logs bytes.Buffer
		opts := silentOptions()
		opts.Jitter = func(time.Duration) time.Duration { return 3 * time.Second }
		opts.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		d := &fakeDriver{poll: func(context.Context) (driver.RawData, error) {
			times = append(times, time.Now())
			if len(times) <= 7 {
				return driver.RawData{}, errors.New("credential-should-not-leak")
			}
			return goodRaw(), nil
		}}
		s := New(nil, nil, func(_ context.Context, r model.SourceResult) error { results = append(results, r); return nil }, opts)
		base := time.Now()
		startMemory(s, source, d)
		synctest.Wait()
		if len(times) != 0 {
			t.Fatal("missing jitter")
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		gaps := []time.Duration{1, 2, 4, 8, 16, 30, 30}
		for _, seconds := range gaps {
			time.Sleep(seconds * time.Second)
			synctest.Wait()
		}
		if len(times) != 8 || len(results) != 8 {
			t.Fatalf("calls=%d results=%d", len(times), len(results))
		}
		if times[0].Sub(base) != 3*time.Second {
			t.Fatal("wrong initial jitter")
		}
		for i, gap := range gaps {
			if times[i+1].Sub(times[i]) != gap*time.Second {
				t.Fatalf("gap %d", i)
			}
		}
		for _, r := range results[:7] {
			if !r.Failed || r.Snapshot.Fields != nil {
				t.Fatal("failure masqueraded as snapshot")
			}
		}
		if results[7].Failed {
			t.Fatal("partial field error rejected")
		}
		st, ok := s.Status(1)
		if !ok || st.Failures != 0 || st.LastOKAt.IsZero() || st.ErrorCode != "" {
			t.Fatalf("status %#v", st)
		}
		time.Sleep(source.PollInterval)
		synctest.Wait()
		if len(times) != 9 {
			t.Fatal("normal cadence not restored")
		}
		if strings.Contains(logs.String(), "credential-should-not-leak") {
			t.Fatal("raw error leaked")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if d.closed.Load() != 1 {
			t.Fatal("driver close count")
		}
		if _, ok := s.Status(1); ok {
			t.Fatal("status retained")
		}
		if _, _, ok := s.Diagnostics(1); ok {
			t.Fatal("cache retained")
		}
	})
}

func TestDeadlinesAndRetries(t *testing.T) {
	for _, tc := range []struct {
		interval, timeout time.Duration
		retries           int
		budget            time.Duration
		calls             int
	}{{10 * time.Second, 2 * time.Second, 2, 6 * time.Second, 3}, {time.Second, 2 * time.Second, 2, 900 * time.Millisecond, 1}} {
		source := testSource(t)
		source.PollInterval = tc.interval
		source.Timeout = tc.timeout
		source.Retries = tc.retries
		synctest.Test(t, func(t *testing.T) {
			count := 0
			var results []model.SourceResult
			d := &fakeDriver{poll: func(ctx context.Context) (driver.RawData, error) {
				count++
				<-ctx.Done()
				return driver.RawData{}, ctx.Err()
			}}
			s := New(nil, nil, func(_ context.Context, r model.SourceResult) error { results = append(results, r); return nil }, silentOptions())
			startMemory(s, source, d)
			synctest.Wait()
			time.Sleep(tc.budget)
			synctest.Wait()
			if count != tc.calls || len(results) != 1 || !results[0].Failed || results[0].Latency != tc.budget {
				t.Fatalf("count=%d results=%#v", count, results)
			}
			st, _ := s.Status(1)
			if st.ErrorCode != "timeout" {
				t.Fatalf("status %#v", st)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNoObservationAndPanic(t *testing.T) {
	for _, panicDriver := range []bool{false, true} {
		source := testSource(t)
		synctest.Test(t, func(t *testing.T) {
			var results []model.SourceResult
			d := &fakeDriver{poll: func(context.Context) (driver.RawData, error) {
				if panicDriver {
					panic("secret panic")
				}
				return driver.RawData{Protocol: driver.ProtocolSNMP, SNMP: map[string]driver.SNMPValue{"1.3.6.1.2.1.33.1.1.2.0": {Tag: 4, Text: "model-only"}}}, nil
			}}
			s := New(nil, nil, func(_ context.Context, r model.SourceResult) error { results = append(results, r); return nil }, silentOptions())
			startMemory(s, source, d)
			synctest.Wait()
			if len(results) != 1 || !results[0].Failed {
				t.Fatal("no observation/panic accepted")
			}
			st, _ := s.Status(1)
			want := "no_observation"
			if panicDriver {
				want = "driver_panic"
			}
			if st.ErrorCode != want {
				t.Fatalf("status=%#v", st)
			}
			err := s.Close()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReloadWaitsAndDropsCanceledResult(t *testing.T) {
	db := newDB(t)
	seed(t, db)
	synctest.Test(t, func(t *testing.T) {
		oldCanceled := make(chan struct{})
		release := make(chan struct{})
		created := 0
		var old *fakeDriver
		var results []model.SourceResult
		opts := silentOptions()
		opts.Factory = func(source Source, _ driver.ReadPlan) (driver.Driver, error) {
			created++
			if created == 1 {
				old = &fakeDriver{poll: func(ctx context.Context) (driver.RawData, error) {
					<-ctx.Done()
					close(oldCanceled)
					<-release
					return goodRaw(), nil
				}}
				return old, nil
			}
			if old.closed.Load() != 1 {
				t.Error("new driver before old closed")
			}
			return &fakeDriver{poll: func(context.Context) (driver.RawData, error) { return goodRaw(), nil }}, nil
		}
		s := New(db, nil, func(_ context.Context, r model.SourceResult) error { results = append(results, r); return nil }, opts)
		if err := s.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		done := make(chan error, 1)
		go func() { done <- s.Reload(context.Background(), 1) }()
		<-oldCanceled
		synctest.Wait()
		if created != 1 {
			t.Fatal("Reload did not wait")
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if len(results) != 1 || results[0].Failed {
			t.Fatalf("stale result published %#v", results)
		}
		execSQL(t, db, `UPDATE sources SET enabled=0 WHERE id=1`)
		if err := s.Reload(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.Status(1); ok {
			t.Fatal("disabled worker retained")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSameIPProbeGateAndClose(t *testing.T) {
	source := testSource(t)
	synctest.Test(t, func(t *testing.T) {
		opts := silentOptions()
		opts.Resolve = func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("::ffff:127.0.0.1")}}, nil
		}
		var probeCalls int
		opts.Factory = func(source Source, _ driver.ReadPlan) (driver.Driver, error) {
			if source.Host != "127.0.0.1" {
				t.Error("host not pinned")
			}
			return &fakeDriver{poll: func(context.Context) (driver.RawData, error) { return goodRaw(), nil }, probe: func(context.Context) (driver.DeviceInfo, driver.RawData, error) {
				probeCalls++
				return driver.DeviceInfo{}, goodRaw(), nil
			}}, nil
		}
		s := New(nil, nil, nil, opts)
		w, err := s.prepare(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		s.sink = func(context.Context, model.SourceResult) error { return nil }
		s.runCtx = context.Background()
		s.started = true
		w.driver = &fakeDriver{poll: func(ctx context.Context) (driver.RawData, error) { <-ctx.Done(); return driver.RawData{}, ctx.Err() }}
		s.launch(w)
		synctest.Wait()
		pending := source
		pending.Host = "same-device.invalid"
		pending.Timeout = 500 * time.Millisecond
		done := make(chan error, 1)
		go func() { _, err := s.Test(context.Background(), pending); done <- err }()
		synctest.Wait()
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if err := <-done; !errors.Is(err, ErrBusy) {
			t.Fatalf("want busy got %v", err)
		}
		if probeCalls != 0 {
			t.Fatal("same IP overlapped")
		}
		// 取消 worker 后 alias 测试可以取得同一把锁。
		w.cancel()
		<-w.done
		if _, err := s.Test(context.Background(), pending); err != nil {
			t.Fatal(err)
		}
		if probeCalls != 1 {
			t.Fatal("probe not called")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Test(context.Background(), source); !errors.Is(err, ErrClosed) {
			t.Fatal("test after close")
		}
	})
}

func TestCloseCancelsTemporaryProbe(t *testing.T) {
	source := testSource(t)
	synctest.Test(t, func(t *testing.T) {
		opts := silentOptions()
		entered := make(chan struct{})
		d := &fakeDriver{probe: func(ctx context.Context) (driver.DeviceInfo, driver.RawData, error) {
			close(entered)
			<-ctx.Done()
			return driver.DeviceInfo{}, driver.RawData{}, ctx.Err()
		}}
		opts.Factory = func(Source, driver.ReadPlan) (driver.Driver, error) { return d, nil }
		s := New(nil, nil, nil, opts)
		done := make(chan error, 1)
		go func() { _, err := s.Test(context.Background(), source); done <- err }()
		<-entered
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
		if d.closed.Load() != 1 {
			t.Fatal("probe not closed")
		}
	})
}

func TestDiagnosticsDeepCopies(t *testing.T) {
	source := testSource(t)
	synctest.Test(t, func(t *testing.T) {
		raw := goodRaw()
		v := raw.SNMP["1.3.6.1.2.1.33.1.2.5.0"]
		v.Bytes = []byte{1, 2}
		raw.SNMP["1.3.6.1.2.1.33.1.2.5.0"] = v
		s := New(nil, nil, func(context.Context, model.SourceResult) error { return nil }, silentOptions())
		startMemory(s, source, &fakeDriver{poll: func(context.Context) (driver.RawData, error) { return raw, nil }})
		synctest.Wait()
		raw.SNMP["1.3.6.1.2.1.33.1.2.5.0"].Bytes[0] = 9
		raw.Errors[0].Kind = "changed"
		r, tr, ok := s.Diagnostics(1)
		if !ok || r.SNMP["1.3.6.1.2.1.33.1.2.5.0"].Bytes[0] != 1 || r.Errors[0].Kind == "changed" {
			t.Fatal("write-side alias")
		}
		r.SNMP["1.3.6.1.2.1.33.1.2.5.0"].Bytes[0] = 7
		r.Errors[0].Kind = "external"
		tr[0].Reason = "external"
		r2, tr2, _ := s.Diagnostics(1)
		if r2.SNMP["1.3.6.1.2.1.33.1.2.5.0"].Bytes[0] != 1 || r2.Errors[0].Kind == "external" || tr2[0].Reason == "external" {
			t.Fatal("read-side alias")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	})
	raw := driver.RawData{Modbus: map[driver.RegKey]uint16{{FC: driver.FC03, Addr: 1}: 2}, AYI9: &driver.AYI9Raw{QAD: []string{"x"}, RTD: []string{"y"}}}
	clone := cloneRaw(raw)
	clone.AYI9.QAD[0] = "z"
	clone.AYI9.RTD[0] = "z"
	clone.Modbus[driver.RegKey{FC: driver.FC03, Addr: 1}] = 9
	if raw.AYI9.QAD[0] != "x" || raw.AYI9.RTD[0] != "y" || raw.Modbus[driver.RegKey{FC: driver.FC03, Addr: 1}] != 2 {
		t.Fatal("protocol alias")
	}
	trace := []normalize.Trace{{Raw: []uint16{1}, Decoded: []byte{2}}}
	copied := cloneTrace(trace)
	copied[0].Raw.([]uint16)[0] = 3
	copied[0].Decoded.([]byte)[0] = 4
	if trace[0].Raw.([]uint16)[0] != 1 || trace[0].Decoded.([]byte)[0] != 2 {
		t.Fatal("trace alias")
	}
}

func TestLoadingConfigAndCredentials(t *testing.T) {
	db := newDB(t)
	seed(t, db)
	cipher, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt([]byte("test-community"), "sources.secret")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, `UPDATE sources SET secret=?, params='{"version":"1"}' WHERE id=1`, encrypted)
	s := New(db, cipher, nil, silentOptions())
	defer s.Close()
	sources, err := s.load(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("load %v", err)
	}
	if sources[0].Credential.Reveal() != "test-community" || sources[0].Params.Version != "1" {
		t.Fatal("config convention mismatch")
	}
	execSQL(t, db, `INSERT INTO sources(id,device_id,protocol,host,port,profile_key,profile_version,poll_ms,timeout_ms) VALUES(2,1,'snmp','127.0.0.1',161,'rfc1628-generic',1,10000,2000)`)
	if _, err := s.load(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("P0 conflict accepted")
	}
	execSQL(t, db, `UPDATE sources SET enabled=0 WHERE id=2`)
	execSQL(t, db, `UPDATE devices SET enabled=0 WHERE id=1`)
	if sources, err := s.load(context.Background()); err != nil || len(sources) != 0 {
		t.Fatal("disabled device selected")
	}
	execSQL(t, db, `UPDATE devices SET enabled=1 WHERE id=1`)
	execSQL(t, db, `UPDATE sources SET profile_version=2 WHERE id=1`)
	if _, err := s.load(context.Background()); !errors.Is(err, ErrConfig) {
		t.Fatal("version mismatch accepted")
	}
	execSQL(t, db, `UPDATE sources SET profile_version=1, secret=? WHERE id=1`, []byte("broken"))
	if _, err := s.load(context.Background()); !errors.Is(err, ErrConfig) {
		t.Fatal("bad credential accepted")
	}
}

func TestStrictParamsAndDNS(t *testing.T) {
	for _, text := range []string{`null`, `[]`, `{"version":null}`, `{"version":"1","version":"2c"}`, `{"strategy":"get"}`, `{"unit_id":1}`, `{} {}`} {
		if _, err := parseParams(text, driver.ProtocolSNMP); err == nil {
			t.Fatalf("accepted %s", text)
		}
	}
	for _, text := range []string{`{"unit_id":256}`, `{"unit_id":-1}`, `{"unit_id":1.5}`, `{"version":"1"}`} {
		if _, err := parseParams(text, driver.ProtocolModbus); err == nil {
			t.Fatalf("accepted %s", text)
		}
	}
	s := New(nil, nil, nil, Options{Resolve: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("127.0.0.2")}}, nil
	}})
	defer s.Close()
	if _, err := s.canonical(context.Background(), "multiple.invalid"); !errors.Is(err, ErrConfig) {
		t.Fatal("multiple DNS addresses accepted")
	}
	if ip, err := s.canonical(context.Background(), "::ffff:127.0.0.1"); err != nil || ip != "127.0.0.1" {
		t.Fatalf("mapped ip %q %v", ip, err)
	}
}
