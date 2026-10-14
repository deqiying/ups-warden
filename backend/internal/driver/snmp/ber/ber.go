// Package ber 实现有界的 SNMP v1/v2c 只读 BER 子集，供驱动、guard 与模拟器共享。
package ber

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"ups-warden/internal/driver"
)

// 仅允许只读请求与响应；没有 SET 编码入口。
const (
	GetRequest     byte = 0xA0
	GetNextRequest byte = 0xA1
	GetBulkRequest byte = 0xA5
	Response       byte = 0xA2
	maxMessage          = 65535
)

// VarBind 保存完整 OID 与原始类型值。
type VarBind struct {
	OID   string
	Value driver.SNMPValue
}

// Message 的 Version 是 wire 值 0/1；GETBULK 的 ErrorStatus/ErrorIndex 是 non-repeaters/max-repetitions。
// RequestIDBytes 仅由 Decode 填充，供模拟器检查短 request-id 怪癖。
type Message struct {
	Version        int
	Community      string
	PDU            byte
	RequestID      int32
	RequestIDBytes int
	ErrorStatus    int
	ErrorIndex     int
	VarBinds       []VarBind
}

// NewRequestID 使用密码学随机数，范围保证最小 BER INTEGER 编码恰为四字节。
func NewRequestID() (int32, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(0x7fffffff-0x00800000+1))
	if err != nil {
		return 0, errors.New("生成 SNMP request-id 失败")
	}
	return int32(n.Int64() + 0x00800000), nil
}
func tlv(tag byte, b []byte) []byte {
	out := []byte{tag}
	n := len(b)
	switch {
	case n < 128:
		out = append(out, byte(n))
	case n < 256:
		out = append(out, 0x81, byte(n))
	default:
		out = append(out, 0x82, byte(n>>8), byte(n))
	}
	return append(out, b...)
}
func signed(n int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(n))
	for len(b) > 1 && ((b[0] == 0 && b[1]&128 == 0) || (b[0] == 255 && b[1]&128 != 0)) {
		b = b[1:]
	}
	return b
}
func unsigned(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	for len(b) > 1 && b[0] == 0 {
		b = b[1:]
	}
	if b[0]&128 != 0 {
		b = append([]byte{0}, b...)
	}
	return b
}
func arcs(s string) ([]uint64, error) {
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 128 {
		return nil, errors.New("无效 OID")
	}
	a := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return nil, errors.New("无效 OID")
		}
		n, e := strconv.ParseUint(p, 10, 32)
		if e != nil {
			return nil, errors.New("无效 OID")
		}
		a[i] = n
	}
	if a[0] > 2 || (a[0] < 2 && a[1] > 39) {
		return nil, errors.New("无效 OID")
	}
	return a, nil
}
func base128(n uint64) []byte {
	b := []byte{byte(n & 127)}
	for n >>= 7; n != 0; n >>= 7 {
		b = append([]byte{byte(n&127) | 128}, b...)
	}
	return b
}
func oidBytes(s string) ([]byte, error) {
	a, e := arcs(s)
	if e != nil {
		return nil, e
	}
	b := base128(40*a[0] + a[1])
	for _, n := range a[2:] {
		b = append(b, base128(n)...)
	}
	return b, nil
}
func oidText(b []byte) (string, error) {
	if len(b) == 0 {
		return "", errors.New("空 OID")
	}
	a := []uint64{}
	var n uint64
	start := true
	for _, v := range b {
		if start && v == 0x80 {
			return "", errors.New("非最小 OID 编码")
		}
		if n > (math.MaxUint32+80)>>7 {
			return "", errors.New("OID 溢出")
		}
		n = n<<7 | uint64(v&127)
		start = false
		if v&128 == 0 {
			if len(a) == 0 {
				switch {
				case n < 40:
					a = append(a, 0, n)
				case n < 80:
					a = append(a, 1, n-40)
				default:
					if n-80 > math.MaxUint32 {
						return "", errors.New("OID 溢出")
					}
					a = append(a, 2, n-80)
				}
			} else {
				if n > math.MaxUint32 {
					return "", errors.New("OID 溢出")
				}
				a = append(a, n)
			}
			if len(a) > 128 {
				return "", errors.New("OID 过长")
			}
			n = 0
			start = true
		}
	}
	if !start {
		return "", errors.New("OID 未结束")
	}
	p := make([]string, len(a))
	for i, n := range a {
		p[i] = strconv.FormatUint(n, 10)
	}
	return strings.Join(p, "."), nil
}

// CompareOID 按数值弧比较合法 OID；非法输入使用字符串序以保持确定性。
func CompareOID(a, b string) int {
	x, e := arcs(a)
	y, f := arcs(b)
	if e != nil || f != nil {
		return strings.Compare(a, b)
	}
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] < y[i] {
			return -1
		}
		if x[i] > y[i] {
			return 1
		}
	}
	if len(x) < len(y) {
		return -1
	}
	if len(x) > len(y) {
		return 1
	}
	return 0
}
func allowed(p byte) bool {
	return p == GetRequest || p == GetNextRequest || p == GetBulkRequest || p == Response
}
func valueBytes(v driver.SNMPValue) ([]byte, error) {
	switch v.Tag {
	case 2:
		return signed(v.Int), nil
	case 4:
		if v.Bytes != nil {
			return v.Bytes, nil
		}
		return []byte(v.Text), nil
	case 5, 0x80, 0x81, 0x82:
		return nil, nil
	case 6:
		return oidBytes(v.Text)
	case 0x41, 0x42, 0x43:
		if v.Uint > math.MaxUint32 {
			return nil, errors.New("32 位计数溢出")
		}
		return unsigned(v.Uint), nil
	case 0x46:
		return unsigned(v.Uint), nil
	default:
		if v.Tag&31 == 31 {
			return nil, errors.New("不支持多字节 tag")
		}
		return v.Bytes, nil
	}
}

// Encode 拒绝写 PDU 和非 NULL 请求值；最终消息限制为 65535 字节。
func Encode(m Message) ([]byte, error) {
	if (m.Version != 0 && m.Version != 1) || !allowed(m.PDU) || m.ErrorStatus < 0 || m.ErrorStatus > math.MaxInt32 || m.ErrorIndex < 0 || m.ErrorIndex > math.MaxInt32 || len(m.Community) > maxMessage || len(m.VarBinds) > 4096 || (m.Version == 0 && m.PDU == GetBulkRequest) {
		return nil, errors.New("无效 SNMP 消息")
	}
	vb := []byte{}
	for _, v := range m.VarBinds {
		o, e := oidBytes(v.OID)
		if e != nil {
			return nil, e
		}
		if m.PDU != Response && (v.Value.Tag != 5 || v.Value.Exception != "" || v.Value.Int != 0 || v.Value.Uint != 0 || v.Value.Text != "" || len(v.Value.Bytes) != 0) {
			return nil, errors.New("只读请求值必须为 NULL")
		}
		b, e := valueBytes(v.Value)
		if e != nil {
			return nil, e
		}
		if len(b) > maxMessage {
			return nil, errors.New("值过长")
		}
		vb = append(vb, tlv(0x30, append(tlv(6, o), tlv(v.Value.Tag, b)...))...)
		if len(vb) > maxMessage {
			return nil, errors.New("消息过长")
		}
	}
	p := append(tlv(2, signed(int64(m.RequestID))), tlv(2, signed(int64(m.ErrorStatus)))...)
	p = append(p, tlv(2, signed(int64(m.ErrorIndex)))...)
	p = append(p, tlv(0x30, vb)...)
	body := append(tlv(2, signed(int64(m.Version))), tlv(4, []byte(m.Community))...)
	body = append(body, tlv(m.PDU, p)...)
	out := tlv(0x30, body)
	if len(out) > maxMessage {
		return nil, errors.New("消息过长")
	}
	return out, nil
}

type reader struct{ b []byte }

func (r *reader) take() (byte, []byte, error) {
	if len(r.b) < 2 {
		return 0, nil, errors.New("截断 BER")
	}
	tag := r.b[0]
	if tag&31 == 31 {
		return 0, nil, errors.New("多字节 tag 不支持")
	}
	n := int(r.b[1])
	h := 2
	if n&128 != 0 {
		k := n & 127
		if k == 0 || k > 2 || len(r.b) < 2+k {
			return 0, nil, errors.New("无效 BER 长度")
		}
		if r.b[2] == 0 {
			return 0, nil, errors.New("非最小 BER 长度")
		}
		n = 0
		for _, v := range r.b[2 : 2+k] {
			n = n<<8 | int(v)
		}
		h += k
		if n < 128 {
			return 0, nil, errors.New("非最小 BER 长度")
		}
	}
	if n > len(r.b)-h {
		return 0, nil, errors.New("截断 BER 内容")
	}
	b := r.b[h : h+n]
	r.b = r.b[h+n:]
	return tag, b, nil
}
func (r *reader) expect(tag byte) ([]byte, error) {
	t, b, e := r.take()
	if e != nil {
		return nil, e
	}
	if t != tag {
		return nil, errors.New("意外 BER tag")
	}
	return b, nil
}
func integer(b []byte) (int64, error) {
	if len(b) == 0 || len(b) > 8 {
		return 0, errors.New("无效 INTEGER")
	}
	if len(b) > 1 && ((b[0] == 0 && b[1]&128 == 0) || (b[0] == 255 && b[1]&128 != 0)) {
		return 0, errors.New("非最小 INTEGER")
	}
	var n int64
	if b[0]&128 != 0 {
		n = -1
	}
	for _, v := range b {
		n = n<<8 | int64(v)
	}
	return n, nil
}
func uintValue(b []byte, max int) (uint64, error) {
	if len(b) == 0 || len(b) > max+1 || b[0]&128 != 0 {
		return 0, errors.New("无效无符号整数")
	}
	if len(b) > 1 && b[0] == 0 {
		if b[1]&128 == 0 {
			return 0, errors.New("非最小无符号整数")
		}
		b = b[1:]
	}
	if len(b) > max {
		return 0, errors.New("无符号整数溢出")
	}
	var n uint64
	for _, v := range b {
		n = n<<8 | uint64(v)
	}
	return n, nil
}
func parseValue(t byte, b []byte) (driver.SNMPValue, error) {
	v := driver.SNMPValue{Tag: t}
	var e error
	switch t {
	case 2:
		v.Int, e = integer(b)
	case 4:
		v.Text = string(b)
		v.Bytes = append([]byte(nil), b...)
	case 5:
		if len(b) != 0 {
			e = errors.New("非空 NULL")
		}
	case 6:
		v.Text, e = oidText(b)
	case 0x41, 0x42, 0x43:
		v.Uint, e = uintValue(b, 4)
	case 0x46:
		v.Uint, e = uintValue(b, 8)
	case 0x80, 0x81, 0x82:
		if len(b) != 0 {
			e = errors.New("非空异常值")
		}
		v.Exception = map[byte]string{0x80: "noSuchObject", 0x81: "noSuchInstance", 0x82: "endOfMibView"}[t]
	default:
		v.Bytes = append([]byte(nil), b...)
	}
	return v, e
}

// Decode 严格消耗所有嵌套内容，接受合法的短 request-id，但拒绝截断、溢出和非最小编码。
func Decode(b []byte) (Message, error) {
	m := Message{}
	if len(b) > maxMessage {
		return m, errors.New("消息过长")
	}
	r := reader{b}
	body, e := r.expect(0x30)
	if e != nil {
		return m, e
	}
	if len(r.b) != 0 {
		return m, errors.New("消息尾部有多余数据")
	}
	r = reader{body}
	readInt := func(r *reader) (int64, int, error) {
		b, e := r.expect(2)
		if e != nil {
			return 0, 0, e
		}
		n, e := integer(b)
		return n, len(b), e
	}
	n, _, e := readInt(&r)
	if e != nil || n < 0 || n > 1 {
		return m, errors.New("无效 SNMP 版本")
	}
	m.Version = int(n)
	c, e := r.expect(4)
	if e != nil {
		return m, e
	}
	m.Community = string(c)
	p, pb, e := r.take()
	if e != nil {
		return m, e
	}
	if !allowed(p) || len(r.b) != 0 || (m.Version == 0 && p == GetBulkRequest) {
		return m, errors.New("无效 SNMP PDU")
	}
	m.PDU = p
	r = reader{pb}
	n, k, e := readInt(&r)
	if e != nil || n < math.MinInt32 || n > math.MaxInt32 {
		return m, errors.New("无效 request-id")
	}
	m.RequestID = int32(n)
	m.RequestIDBytes = k
	n, _, e = readInt(&r)
	if e != nil || n < 0 || n > math.MaxInt32 {
		return m, errors.New("无效 error-status")
	}
	m.ErrorStatus = int(n)
	n, _, e = readInt(&r)
	if e != nil || n < 0 || n > math.MaxInt32 {
		return m, errors.New("无效 error-index")
	}
	m.ErrorIndex = int(n)
	list, e := r.expect(0x30)
	if e != nil {
		return m, e
	}
	if len(r.b) != 0 {
		return m, errors.New("PDU 多余内容")
	}
	r = reader{list}
	for len(r.b) > 0 {
		if len(m.VarBinds) >= 4096 {
			return m, errors.New("过多 varbind")
		}
		b, e := r.expect(0x30)
		if e != nil {
			return m, e
		}
		v := reader{b}
		o, e := v.expect(6)
		if e != nil {
			return m, e
		}
		s, e := oidText(o)
		if e != nil {
			return m, e
		}
		t, b, e := v.take()
		if e != nil {
			return m, e
		}
		if len(v.b) != 0 {
			return m, errors.New("varbind 多余内容")
		}
		value, e := parseValue(t, b)
		if e != nil {
			return m, fmt.Errorf("varbind: %w", e)
		}
		if p != Response && t != 5 {
			return m, errors.New("请求值必须为 NULL")
		}
		m.VarBinds = append(m.VarBinds, VarBind{OID: s, Value: value})
	}
	return m, nil
}
