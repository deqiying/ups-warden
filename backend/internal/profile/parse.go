package profile

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
)

const maxDocumentBytes = 256 * 1024

// Parse 严格加载单个 YAML/JSON 文档，填充缺省值并执行语义校验。
func Parse(data []byte) (*Profile, error) {
	if len(data) > maxDocumentBytes {
		return nil, fmt.Errorf("line 1: $: document exceeds 256 KiB")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("$: %w", err)
	}
	if len(root.Content) != 1 {
		return nil, fmt.Errorf("line 1: $: expected profile object")
	}
	lines := map[string]int{}
	if err := checkNode(root.Content[0], reflect.TypeFor[Profile](), "", lines); err != nil {
		return nil, err
	}
	var trailing yaml.Node
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("line %d: $: trailing document (%v)", max(1, trailing.Line), err)
	}
	p := &Profile{lines: lines}
	// 在解码前初始化：显式零值不会被误当作省略。
	n := root.Content[0]
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == "protocol" {
			switch n.Content[i+1].Value {
			case "snmp":
				p.SNMP = defaultSNMP()
			case "modbus":
				p.Modbus = defaultModbus()
			case "ayi9-json":
				p.AYI9 = defaultAYI9()
			}
		}
	}
	if err := n.Decode(p); err != nil {
		return nil, fmt.Errorf("$: %w", err)
	}
	for i := range p.Fields {
		if p.Fields[i].Scale == nil {
			v := 1.0
			p.Fields[i].Scale = &v
		}
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func checkNode(n *yaml.Node, t reflect.Type, path string, lines map[string]int) error {
	fail := func(s string) error { return fmt.Errorf("line %d: %s: %s", n.Line, displayPath(path), s) }
	lines[path] = n.Line
	if n.Kind == yaml.AliasNode || n.Tag == "!!merge" {
		return fail("aliases and merges are not allowed")
	}
	if n.Tag == "!!null" {
		return fail("null is not allowed")
	}
	if n.Tag != "!!map" && n.Tag != "!!seq" && n.Tag != "!!str" && n.Tag != "!!int" && n.Tag != "!!float" && n.Tag != "!!bool" {
		return fail("unsupported YAML tag")
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if n.Kind != yaml.MappingNode {
			return fail("expected object")
		}
		keys := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			key := strings.Split(f.Tag.Get("yaml"), ",")[0]
			if key == "" {
				key = strings.ToLower(f.Name)
			}
			keys[key] = f.Type
		}
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			child := k.Value
			if path != "" {
				child = path + "." + child
			}
			lines[child] = v.Line
			if k.Kind != yaml.ScalarNode || k.Tag == "!!merge" {
				return fmt.Errorf("line %d: %s: invalid key", k.Line, child)
			}
			if seen[k.Value] {
				return fmt.Errorf("line %d: %s: duplicate key", k.Line, child)
			}
			seen[k.Value] = true
			ft, ok := keys[k.Value]
			if !ok {
				return fmt.Errorf("line %d: %s: unknown field", k.Line, child)
			}
			if err := checkNode(v, ft, child, lines); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			return fail("expected sequence")
		}
		for i, v := range n.Content {
			if err := checkNode(v, t.Elem(), fmt.Sprintf("%s[%d]", path, i), lines); err != nil {
				return err
			}
		}
	case reflect.Map:
		if n.Kind != yaml.MappingNode {
			return fail("expected object")
		}
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode || k.Tag == "!!null" || k.Tag == "!!merge" || seen[k.Value] {
				return fmt.Errorf("line %d: %s: invalid or duplicate key", k.Line, path)
			}
			seen[k.Value] = true
			if err := checkNode(v, t.Elem(), path+"."+k.Value, lines); err != nil {
				return err
			}
		}
	default:
		if n.Kind != yaml.ScalarNode {
			return fail("expected scalar")
		}
		if t.Kind() == reflect.String && n.Tag != "!!str" {
			// invalid.values 允许 YAML/JSON 数字转为原始比较文本，其余文本必须显式为字符串。
			if !strings.Contains(path, ".invalid.values[") || (n.Tag != "!!int" && n.Tag != "!!float") {
				return fail("expected string")
			}
			// 数字节点按 YAML 数值语义转为十进制文本，不能让 0xFFFF 等词法形式
			// 在归一化阶段被当成普通字符串而跳过哨兵比较；带引号字符串保持原样。
			var number any
			if err := n.Decode(&number); err != nil {
				return fail(err.Error())
			}
			n.Value, n.Tag = fmt.Sprint(number), "!!str"
		}
		if t.Kind() == reflect.Int && n.Tag != "!!int" {
			return fail("expected integer")
		}
		if t.Kind() == reflect.Bool && n.Tag != "!!bool" {
			return fail("expected boolean")
		}
		if t.Kind() == reflect.Float64 && n.Tag != "!!float" && n.Tag != "!!int" {
			return fail("expected number")
		}
		if err := n.Decode(reflect.New(t).Interface()); err != nil {
			return fail(err.Error())
		}
	}
	return nil
}
func displayPath(s string) string {
	if s == "" {
		return "$"
	}
	return s
}
func defaultSNMP() *SNMPConfig {
	return &SNMPConfig{Strategy: "getbulk", BulkRoot: "1.3.6.1.2.1.33", MaxRepetitions: 60}
}
func defaultModbus() *ModbusConfig {
	return &ModbusConfig{MaxRegistersPerRead: 100, ByteOrder: "big", WordOrder: "big"}
}
func defaultAYI9() *AYI9Config { return &AYI9Config{RTDIntervalSeconds: 600} }
func (p *Profile) fail(path, msg string) error {
	line := 0
	if p != nil {
		line = p.lines[""]
		for q := path; ; {
			if v, ok := p.lines[q]; ok {
				line = v
				break
			}
			i := strings.LastIndexAny(q, ".[")
			if i < 0 {
				break
			}
			q = q[:i]
		}
	}
	return fmt.Errorf("line %d: %s: %s", line, displayPath(path), msg)
}

// 程序构造的空协议配置沿用缺省值；Parse 的显式值仍需通过严格校验。
func (p *Profile) snmpConfig() *SNMPConfig {
	c := defaultSNMP()
	if p.SNMP != nil {
		*c = *p.SNMP
	}
	if p.lines == nil {
		if c.Strategy == "" {
			c.Strategy = "getbulk"
		}
		if c.BulkRoot == "" {
			c.BulkRoot = "1.3.6.1.2.1.33"
		}
		if c.MaxRepetitions == 0 {
			c.MaxRepetitions = 60
		}
	}
	return c
}
func (p *Profile) ayi9Config() *AYI9Config {
	c := defaultAYI9()
	if p.AYI9 != nil {
		*c = *p.AYI9
	}
	if p.lines == nil && c.RTDIntervalSeconds == 0 {
		c.RTDIntervalSeconds = 600
	}
	return c
}
