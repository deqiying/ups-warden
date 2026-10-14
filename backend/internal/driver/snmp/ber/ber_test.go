package ber

import (
	"bytes"
	"math"
	"reflect"
	"testing"
	"ups-warden/internal/driver"
)

func TestRequestIDsAndReadOnly(t *testing.T) {
	for i := 0; i < 100; i++ {
		id, e := NewRequestID()
		if e != nil {
			t.Fatal(e)
		}
		if id < 0x00800000 {
			t.Fatal(id)
		}
		m := Message{Version: 1, Community: "test-only", PDU: GetRequest, RequestID: id, VarBinds: []VarBind{{OID: "1.3.6.1.2.1.33.1.2.1.0", Value: driver.SNMPValue{Tag: 5}}}}
		b, e := Encode(m)
		if e != nil {
			t.Fatal(e)
		}
		r, e := Decode(b)
		if e != nil || r.RequestIDBytes != 4 || r.RequestID != id {
			t.Fatalf("%+v %v", r, e)
		}
		if !bytes.Contains(b, append([]byte{2, 4}, signed(int64(id))...)) {
			t.Fatal("missing four-byte id")
		}
	}
	for _, p := range []byte{0xA3, 0xA4, 0xA6, 0xA7, 0xA8} {
		if _, e := Encode(Message{PDU: p}); e == nil {
			t.Fatalf("accepted %x", p)
		}
	}
	if _, e := Encode(Message{PDU: GetRequest, VarBinds: []VarBind{{OID: "1.3", Value: driver.SNMPValue{Tag: 2}}}}); e == nil {
		t.Fatal("accepted non-null")
	}
	b, e := Encode(Message{Version: 0, PDU: GetNextRequest, RequestID: 1})
	if e != nil {
		t.Fatal(e)
	}
	m, e := Decode(b)
	if e != nil || m.RequestIDBytes != 1 {
		t.Fatal(m, e)
	}
}
func TestValueRoundTrip(t *testing.T) {
	vals := []driver.SNMPValue{{Tag: 2, Int: math.MinInt64}, {Tag: 2, Int: math.MaxInt64}, {Tag: 4, Text: "UPS"}, {Tag: 5}, {Tag: 6, Text: "2.4294967295.128.0"}, {Tag: 0x41, Uint: math.MaxUint32}, {Tag: 0x42, Uint: 128}, {Tag: 0x43, Uint: 0}, {Tag: 0x46, Uint: math.MaxUint64}, {Tag: 0x80, Exception: "noSuchObject"}, {Tag: 0x81, Exception: "noSuchInstance"}, {Tag: 0x82, Exception: "endOfMibView"}, {Tag: 0x44, Bytes: []byte{1, 2, 255}}}
	for _, v := range vals {
		m := Message{Version: 1, PDU: Response, RequestID: 0x800000, VarBinds: []VarBind{{OID: "1.3.6.1.2.1.33.0", Value: v}}}
		b, e := Encode(m)
		if e != nil {
			t.Fatal(e)
		}
		r, e := Decode(b)
		if e != nil {
			t.Fatal(e)
		}
		got := r.VarBinds[0].Value
		if v.Tag == 4 {
			v.Bytes = []byte(v.Text)
		}
		if !reflect.DeepEqual(v, got) {
			t.Fatalf("want %+v got %+v", v, got)
		}
	}
	if CompareOID("1.3.6.10", "1.3.6.2") <= 0 || CompareOID("1.3", "1.3.0") >= 0 {
		t.Fatal("lexical OID order")
	}
}
func TestMalformed(t *testing.T) {
	b, e := Encode(Message{Version: 1, PDU: Response, RequestID: 0x800000, VarBinds: []VarBind{{OID: "1.3.6", Value: driver.SNMPValue{Tag: 5}}}})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < len(b); i++ {
		if _, e := Decode(b[:i]); e == nil {
			t.Fatalf("accepted truncation %d", i)
		}
	}
	for _, bad := range [][]byte{append(append([]byte(nil), b...), 0), {0x30, 0x80, 0, 0}, {0x30, 0x82, 0, 1, 0}, {0x30, 0x81, 1, 0}, {0x30, 0xff}, {0x30, 0x82, 0xff, 0xff}} {
		if _, e := Decode(bad); e == nil {
			t.Fatalf("accepted %x", bad)
		}
	}
	for _, o := range []string{"", "1", "3.1", "1.40", "1.03", "1.+3", "1.4294967296", "1.3.", "1.3.-1"} {
		if _, e := oidBytes(o); e == nil {
			t.Fatalf("accepted %q", o)
		}
	}
	for _, b := range [][]byte{{0x80, 1}, {0x81}, {0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}} {
		if _, e := oidText(b); e == nil {
			t.Fatal("bad OID accepted")
		}
	}
}
func FuzzDecode(f *testing.F) {
	b, _ := Encode(Message{Version: 1, PDU: Response, RequestID: 0x800000, VarBinds: []VarBind{{OID: "1.3.6.1.2.1.33", Value: driver.SNMPValue{Tag: 0x46, Uint: math.MaxUint64}}}})
	f.Add(b)
	f.Add([]byte{0x30, 0x80})
	f.Fuzz(func(t *testing.T, b []byte) {
		m, e := Decode(b)
		if e != nil {
			return
		}
		out, e := Encode(m)
		if e != nil {
			t.Fatalf("decoded message cannot encode: %v", e)
		}
		r, e := Decode(out)
		if e != nil {
			t.Fatal(e)
		}
		m.RequestIDBytes = r.RequestIDBytes
		if !reflect.DeepEqual(m, r) {
			t.Fatalf("round-trip changed message: %+v %+v", m, r)
		}
	})
}
func FuzzEncode(f *testing.F) {
	f.Add("1.3.6.1.2.1.33", uint8(4), int64(-1), uint64(1), []byte("value"))
	f.Fuzz(func(t *testing.T, o string, tag uint8, n int64, u uint64, b []byte) {
		m := Message{Version: 1, PDU: Response, RequestID: 0x800000, VarBinds: []VarBind{{OID: o, Value: driver.SNMPValue{Tag: tag, Int: n, Uint: u, Bytes: b, Text: string(b)}}}}
		out, e := Encode(m)
		if e == nil {
			if _, e := Decode(out); e != nil {
				t.Fatalf("encoder emitted malformed data: %v", e)
			}
		}
	})
}
