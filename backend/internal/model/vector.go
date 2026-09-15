package model

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
)

// Vector pgvector 向量类型 (M11, 计划 §3): GORM 字段类型, 零新依赖。
// 可空列 (KBDocument.Embedding) 用 *Vector 表示 NULL:
//   - 写库: Vector.Value() 输出 pgvector 文本格式 "[0.1,0.2,...]"; nil 指针输出 NULL
//   - 读库: Vector.Scan() 解析 "[...]" 文本; NULL 时 V 置空 (用 IsNull 判定)
type Vector struct {
	V []float32
}

// IsNull 判定向量是否为空 (NULL 或未填充)
func (v *Vector) IsNull() bool {
	return v == nil || len(v.V) == 0
}

// Value 实现 driver.Valuer
func (v Vector) Value() (driver.Value, error) {
	if len(v.V) == 0 {
		return nil, nil
	}
	parts := make([]string, len(v.V))
	for i, f := range v.V {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

// Scan 实现 sql.Scanner: 兼容 string / []byte / nil (NULL)
func (v *Vector) Scan(src interface{}) error {
	if src == nil {
		v.V = nil
		return nil
	}
	var s string
	switch t := src.(type) {
	case string:
		s = t
	case []byte:
		s = string(t)
	default:
		return fmt.Errorf("vector: unsupported scan source type %T", src)
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		v.V = nil
		return nil
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	inner := strings.TrimSpace(s)
	if inner == "" {
		v.V = nil
		return nil
	}
	fields := strings.Split(inner, ",")
	out := make([]float32, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		val, err := strconv.ParseFloat(f, 32)
		if err != nil {
			return fmt.Errorf("vector: parse %q: %w", f, err)
		}
		out = append(out, float32(val))
	}
	v.V = out
	return nil
}
