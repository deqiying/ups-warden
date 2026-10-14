package normalize

import (
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"strings"

	"ups-warden/internal/profile"
)

func decode(raw any, kind string, config *profile.ModbusConfig) (any, error) {
	fail := errors.New("原始数据类型或值不符合映射")
	if words, ok := raw.([]uint16); ok {
		if config == nil || len(words) < 1 || len(words) > 2 {
			return nil, fail
		}
		data := make([]byte, len(words)*2)
		for i, word := range words {
			if config.ByteOrder == "little" {
				binary.LittleEndian.PutUint16(data[i*2:], word)
			} else {
				binary.BigEndian.PutUint16(data[i*2:], word)
			}
		}
		if len(words) == 2 && config.WordOrder == "little" {
			data[0], data[1], data[2], data[3] = data[2], data[3], data[0], data[1]
		}
		var number float64
		switch kind {
		case "uint16", "int":
			if len(data) != 2 {
				return nil, fail
			}
			number = float64(binary.BigEndian.Uint16(data))
		case "int16":
			if len(data) != 2 {
				return nil, fail
			}
			number = float64(int16(binary.BigEndian.Uint16(data)))
		case "uint32":
			if len(data) != 4 {
				return nil, fail
			}
			number = float64(binary.BigEndian.Uint32(data))
		case "int32":
			if len(data) != 4 {
				return nil, fail
			}
			number = float64(int32(binary.BigEndian.Uint32(data)))
		case "float32":
			if len(data) != 4 {
				return nil, fail
			}
			number = float64(math.Float32frombits(binary.BigEndian.Uint32(data)))
		default:
			return nil, fail
		}
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fail
		}
		return number, nil
	}
	if kind == "string" {
		text, ok := raw.(string)
		if !ok {
			return nil, fail
		}
		return strings.TrimSpace(text), nil
	}
	if kind == "bitstring16" {
		text, ok := raw.(string)
		if !ok {
			return nil, fail
		}
		text = strings.TrimSpace(text)
		if len(text) != 16 || strings.Trim(text, "01") != "" {
			return nil, fail
		}
		number, err := strconv.ParseUint(text, 2, 16)
		if err != nil {
			return nil, fail
		}
		return float64(number), nil
	}
	if kind == "number_string" || kind == "si_number" {
		text, ok := raw.(string)
		if !ok {
			return nil, fail
		}
		text = strings.TrimSpace(text)
		factor := 1.0
		if kind == "si_number" && len(text) > 0 {
			switch text[len(text)-1] {
			case 'K', 'k':
				factor = 1000
				text = text[:len(text)-1]
			case 'M':
				factor = 1000000
				text = text[:len(text)-1]
			}
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number*factor, 0) {
			return nil, fail
		}
		return number * factor, nil
	}
	var number float64
	switch value := raw.(type) {
	case int64:
		number = float64(value)
	case uint64:
		number = float64(value)
	case string:
		// int 是明确的十进制整数类型，不接受 1.2、十六进制或非数值文本。
		value = strings.TrimSpace(value)
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fail
		}
		number = float64(n)
	default:
		return nil, fail
	}
	switch kind {
	case "int":
	case "uint16":
		if number < 0 || number > math.MaxUint16 {
			return nil, fail
		}
	case "int16":
		if number < math.MinInt16 || number > math.MaxInt16 {
			return nil, fail
		}
	case "uint32":
		if number < 0 || number > math.MaxUint32 {
			return nil, fail
		}
	case "int32":
		if number < math.MinInt32 || number > math.MaxInt32 {
			return nil, fail
		}
	default:
		return nil, fail
	}
	return number, nil
}
