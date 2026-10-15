package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/ayi9"
)

func TestSourceTimingRejectsUnsupportedValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Source)
	}{
		{"short_poll", func(s *Source) { s.PollInterval = 199 * time.Millisecond }},
		{"long_poll", func(s *Source) { s.PollInterval = time.Hour + time.Millisecond }},
		{"tiny_poll", func(s *Source) { s.PollInterval = 11 * time.Nanosecond }},
		{"extreme_poll", func(s *Source) { s.PollInterval = time.Duration(1<<63 - 1) }},
		{"short_timeout", func(s *Source) { s.Timeout = time.Microsecond }},
		{"long_timeout", func(s *Source) { s.Timeout = time.Minute + time.Millisecond }},
		{"negative_retries", func(s *Source) { s.Retries = -1 }},
		{"excess_retries", func(s *Source) { s.Retries = 11 }},
		{"extreme_retries", func(s *Source) { s.Retries = int(^uint(0) >> 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := testSource(t)
			tc.change(&source)
			if !errors.Is(ValidateSource(source), ErrConfig) {
				t.Fatal("应在构造驱动前拒绝超限配置")
			}
		})
	}
}

func TestLoadRejectsTimingBeforeDurationConversion(t *testing.T) {
	db := newDB(t)
	seed(t, db)
	s := New(db, nil, nil, silentOptions())
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, tc := range []struct {
		poll, timeout int64
		retries       int
	}{
		{199, 1000, 0}, {3600001, 1000, 0}, {1<<63 - 1, 1000, 0},
		{3000, 60001, 0}, {3000, 1<<63 - 1, 0}, {3000, 1000, 11},
	} {
		execSQL(t, db, "UPDATE sources SET poll_ms=?,timeout_ms=?,retries=?", tc.poll, tc.timeout, tc.retries)
		if _, err := s.load(t.Context()); !errors.Is(err, ErrConfig) {
			t.Fatalf("未拒绝配置: %v", err)
		}
	}
}

func TestInvalidProfileRejectedBeforeFactory(t *testing.T) {
	source := testSource(t)
	source.Profile.Fields[0].Field = "not_a_field"
	opts := silentOptions()
	opts.Factory = func(Source, driver.ReadPlan) (driver.Driver, error) {
		t.Fatal("非法 Profile 不应进入驱动构造")
		return nil, nil
	}
	s := New(nil, nil, nil, opts)
	defer s.Close()
	if _, err := s.Test(t.Context(), source); !errors.Is(err, ErrConfig) {
		t.Fatalf("应在采集边界拒绝非法 Profile: %v", err)
	}
}

func TestDriverErrorKeepsSafeClassification(t *testing.T) {
	for _, original := range []error{
		fmt.Errorf("private-password: %w", &ayi9.DeviceError{Code: 103}),
		fmt.Errorf("private-password: %w", ayi9.ErrPasswordWrong),
		fmt.Errorf("private-password: %w", &ayi9.DeviceError{Code: 201}),
		errors.New("private-password"),
	} {
		d := &fakeDriver{poll: func(context.Context) (driver.RawData, error) { return driver.RawData{}, original }}
		_, pollErr := pollDriver(t.Context(), d)
		_, _, probeErr := probeDriver(t.Context(), d)
		for _, err := range []error{pollErr, probeErr} {
			if !errors.Is(err, ErrDriver) || strings.Contains(err.Error(), "private-password") {
				t.Fatalf("错误未安全分类: %v", err)
			}
			if errors.Is(err, ErrPasswordWrong) != errors.Is(original, ayi9.ErrPasswordWrong) {
				t.Fatal("密码错误分类丢失")
			}
			var device *ayi9.DeviceError
			if errors.As(original, &device) {
				var safe *ayi9.DeviceError
				if !errors.As(err, &safe) || safe.Code != device.Code {
					t.Fatal("设备错误码丢失")
				}
			}
			if errors.Is(err, ErrPasswordWrong) && errorCode(err) != "password_wrong" {
				t.Fatal("运行状态未区分密码错误")
			}
		}
	}
}
