package hinge

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 本文件的解析助手供「生成的绑定器」直接调用：从原始字符串到强类型入参
// 的全部解析都是生成代码中的直白赋值 + 本文件的无反射助手，请求期零反射。
// 语义与 v0.1 反射绑定（SetRaw/SetSliceValue）逐条对齐：
//   - 基本类型 / time.Time（RFC3339）/ 指针由生成代码分配 / 切片（重复参数 + 逗号串等价）
//   - 错误信息形态保持 "invalid <name>: <raw>"

// valueT 标量绑定支持的类型集合（与 v0.1 SetRawBasic 一致）。
type valueT interface {
	string | bool |
		int | int8 | int16 | int32 | int64 |
		uint | uint8 | uint16 | uint32 | uint64 |
		float32 | float64 |
		time.Time
}

// Flat 展开参数值列表：重复参数与逗号分隔等价（?ids=1,2 ≡ ?ids=1&ids=2）。
func Flat(vals []string) []string {
	if len(vals) == 1 && !strings.Contains(vals[0], ",") {
		return vals
	}
	var out []string
	for _, v := range vals {
		out = append(out, strings.Split(v, ",")...)
	}
	return out
}

// Parse 把原始字符串解析为 T（生成的绑定器逐字段调用）。
func Parse[T valueT](raw, name string) (T, error) {
	var zero T
	switch p := any(&zero).(type) {
	case *string:
		*p = raw
	case *bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = v
	case *time.Time:
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s (want RFC3339)", name, raw)
		}
		*p = v
	case *int:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = int(v)
	case *int8:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = int8(v)
	case *int16:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = int16(v)
	case *int32:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = int32(v)
	case *int64:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = v
	case *uint:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = uint(v)
	case *uint8:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = uint8(v)
	case *uint16:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = uint16(v)
	case *uint32:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = uint32(v)
	case *uint64:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = v
	case *float32:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = float32(v)
	case *float64:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return zero, fmt.Errorf("invalid %s: %s", name, raw)
		}
		*p = v
	default:
		return zero, fmt.Errorf("invalid %s: unsupported field type %T", name, zero)
	}
	return zero, nil
}

// ParseSlice 解析切片字段：每个元素经 Parse[T]；空串元素跳过语义由生成代码处理
// （空串视为缺失，不会进入本函数）。
func ParseSlice[T valueT](vals []string, name string) ([]T, error) {
	out := make([]T, 0, len(vals))
	for _, raw := range vals {
		v, err := Parse[T](raw, name)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
