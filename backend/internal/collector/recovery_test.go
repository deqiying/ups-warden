package collector

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
)

func TestCollectionKeepsMonotonicClock(t *testing.T) {
	results := make(chan model.SourceResult, 1)
	s := New(nil, nil, func(_ context.Context, r model.SourceResult) error { results <- r; return nil }, silentOptions())
	startMemory(s, testSource(t), &fakeDriver{poll: func(context.Context) (driver.RawData, error) { return goodRaw(), nil }})
	defer s.Close()
	select {
	case r := <-results:
		// Round(0) 明确移除单调读数；生产链路不能提前做 UTC/序列化转换。
		if r.At == r.At.Round(0) {
			t.Fatal("采集事实丢失单调时钟")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("未采集")
	}
}

func TestPanicRebuildsWithBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var results []model.SourceResult
		var drivers []*fakeDriver
		panicDriver := func() *fakeDriver {
			return &fakeDriver{poll: func(context.Context) (driver.RawData, error) { panic("sensitive") }}
		}
		first := panicDriver()
		drivers = append(drivers, first)
		opts := silentOptions()
		created := 0
		opts.Factory = func(Source, driver.ReadPlan) (driver.Driver, error) {
			created++
			d := panicDriver()
			if created == 3 {
				d = &fakeDriver{poll: func(context.Context) (driver.RawData, error) { return goodRaw(), nil }}
			}
			drivers = append(drivers, d)
			return d, nil
		}
		s := New(nil, nil, func(_ context.Context, r model.SourceResult) error { results = append(results, r); return nil }, opts)
		defer s.Close()
		startMemory(s, testSource(t), first)
		synctest.Wait()
		for _, d := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
			time.Sleep(d)
			synctest.Wait()
		}
		if len(results) != 4 || results[3].Failed || created != 3 {
			t.Fatalf("panic 后未按退避恢复: results=%d rebuilt=%d", len(results), created)
		}
		for _, r := range results[:3] {
			if !r.Failed {
				t.Fatal("panic 被当作成功")
			}
		}
		st, _ := s.Status(1)
		if st.Failures != 0 || st.ErrorCode != "" {
			t.Fatal("恢复未清理状态")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		for _, d := range drivers {
			if d.closed.Load() != 1 {
				t.Fatal("驱动未恰好关闭一次")
			}
		}
	})
}
