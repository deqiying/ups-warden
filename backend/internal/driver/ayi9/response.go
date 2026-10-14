package ayi9

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

type response struct {
	session, cmd, uid, value string
	code                     int
}

func parseResponse(packet []byte) (response, error) {
	var r response
	fail := func() (response, error) { return response{}, errors.New("AYI9 响应对象字段非法") }
	if len(packet) > 64*1024 {
		return fail()
	}
	dec := json.NewDecoder(bytes.NewReader(packet))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return fail()
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return fail()
		}
		key, ok := tok.(string)
		if !ok {
			return fail()
		}
		if _, exists := fields[key]; exists {
			return fail()
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return fail()
		}
		fields[key] = value
	}
	tok, err = dec.Token()
	if err != nil || tok != json.Delim('}') {
		return fail()
	}
	if _, err = dec.Token(); err != io.EOF {
		return fail()
	}
	for key, dst := range map[string]*string{"session": &r.session, "cmd": &r.cmd, "uid": &r.uid} {
		if data, ok := fields[key]; ok {
			if bytes.Equal(data, []byte("null")) || json.Unmarshal(data, dst) != nil {
				return fail()
			}
		}
	}
	if r.session == "" {
		return fail()
	}
	if data, ok := fields["code"]; ok {
		code, ok := integer(data)
		if !ok || code < 0 || code > 65535 {
			return fail()
		}
		r.code = code
	}
	value, exists := fields["value"]
	if exists {
		if len(value) > 0 && value[0] == '"' {
			if json.Unmarshal(value, &r.value) != nil {
				return fail()
			}
		} else {
			code, ok := integer(value)
			if !ok || !knownCode(code) {
				return fail()
			}
			r.value = strconv.Itoa(code)
		}
		if code, err := strconv.Atoi(strings.TrimSpace(r.value)); err == nil && knownCode(code) {
			if r.code != 0 && r.code != code {
				return fail()
			}
			r.code = code
		}
	}
	if !exists && r.code == 0 {
		return fail()
	}
	return r, nil
}
func integer(data []byte) (int, bool) {
	var value string
	if len(data) > 0 && data[0] == '"' {
		if json.Unmarshal(data, &value) != nil {
			return 0, false
		}
	} else {
		value = string(data)
	}
	n, err := strconv.Atoi(value)
	return n, err == nil
}
func knownCode(n int) bool { return n >= 101 && n <= 106 || n == 201 || n == 410 }
