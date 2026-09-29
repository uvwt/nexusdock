package agentdock

import (
	"fmt"
	"strconv"
	"strings"
)

// ContractError 表示 AgentDock 节点返回的 Runtime 数据与 Nexus 期望的契约不一致。
// 此前 httpx 直接用 opsString/opsInt 解析上游动态 map，字段类型漂移时静默变成零值，
// 用户只会看到空列表或 0；现在在边界处显式报错，错误信息携带节点、上游方法与字段路径便于定位。
type ContractError struct {
	Node      string // AgentDock 节点 ID
	Operation string // 上游 Runtime API 调用，如 "GET /internal/runtime/tasks"
	Field     string // 字段路径，如 "tasks[0].status"
	Reason    string // 具体原因，如 "缺少必填字段" / "应为字符串"
}

func (e *ContractError) Error() string {
	return fmt.Sprintf("AgentDock 节点 %s 的 %s 响应契约不一致（字段 %s）: %s", e.Node, e.Operation, e.Field, e.Reason)
}

// runtimeParser 在一次上游调用范围内生成带定位上下文的契约错误。
// 它只覆盖 Nexus 真正消费的字段：未使用的上游字段不解析也不校验，避免把整个上游响应锁死。
type runtimeParser struct {
	node      string
	operation string
}

func (p runtimeParser) fail(field, reason string) error {
	return &ContractError{Node: p.node, Operation: p.operation, Field: field, Reason: reason}
}

// object 断言 value 是 JSON 对象；absentOK 时允许缺失或 null 并返回 nil。
func (p runtimeParser) object(field string, value any, absentOK bool) (map[string]any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, nil
	case nil:
		if absentOK {
			return nil, nil
		}
		return nil, p.fail(field, "缺少必填对象")
	default:
		return nil, p.fail(field, fmt.Sprintf("应为对象，实际为 %T", value))
	}
}

// array 断言 value 是 JSON 数组；absentOK 时允许缺失或 null 并返回 nil。
func (p runtimeParser) array(field string, value any, absentOK bool) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case nil:
		if absentOK {
			return nil, nil
		}
		return nil, p.fail(field, "缺少必填数组")
	default:
		return nil, p.fail(field, fmt.Sprintf("应为数组，实际为 %T", value))
	}
}

// requiredString 断言 value 是非空字符串。
func (p runtimeParser) requiredString(field string, value any) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", p.fail(field, fmt.Sprintf("应为字符串，实际为 %T", value))
	}
	if strings.TrimSpace(text) == "" {
		return "", p.fail(field, "缺少必填字段")
	}
	return text, nil
}

// optionalString 在字段缺失或为 null 时返回空串；存在但类型不对时报契约错误。
func (p runtimeParser) optionalString(field string, value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	default:
		return "", p.fail(field, fmt.Sprintf("应为字符串，实际为 %T", value))
	}
}

// optionalInt 在字段缺失或为 null 时返回 0；存在时必须是整数（上游 JSON 数字解码为 float64）。
func (p runtimeParser) optionalInt(field string, value any) (int, error) {
	switch typed := value.(type) {
	case nil:
		return 0, nil
	case float64:
		if typed != float64(int(typed)) {
			return 0, p.fail(field, fmt.Sprintf("应为整数，实际为 %v", typed))
		}
		return int(typed), nil
	case int:
		return typed, nil
	case int64:
		return int(typed), nil
	default:
		return 0, p.fail(field, fmt.Sprintf("应为整数，实际为 %T", value))
	}
}

// optionalInt64 与 optionalInt 相同，用于文件大小等可能超过 int32 的字段。
func (p runtimeParser) optionalInt64(field string, value any) (int64, error) {
	number, err := p.optionalInt(field, value)
	if err != nil {
		return 0, err
	}
	return int64(number), nil
}

// optionalBool 在字段缺失或为 null 时返回 false；存在但类型不对时报契约错误。
func (p runtimeParser) optionalBool(field string, value any) (bool, error) {
	switch typed := value.(type) {
	case nil:
		return false, nil
	case bool:
		return typed, nil
	default:
		return false, p.fail(field, fmt.Sprintf("应为布尔值，实际为 %T", value))
	}
}

func (p runtimeParser) requiredBool(field string, value any) (bool, error) {
	typed, ok := value.(bool)
	if !ok {
		return false, p.fail(field, fmt.Sprintf("应为布尔值，实际为 %T", value))
	}
	return typed, nil
}

func (p runtimeParser) requiredNumber(field string, value any) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	default:
		return 0, p.fail(field, fmt.Sprintf("应为数值，实际为 %T", value))
	}
}

// optionalStringArray 在字段缺失或为 null 时返回空切片；元素必须是字符串。
// 返回非 nil 空切片，保持与旧实现 opsStringArray 一致的序列化行为（[] 而不是 null）。
func (p runtimeParser) optionalStringArray(field string, value any) ([]string, error) {
	return p.stringArray(field, value, true)
}

// requiredStringArray 断言 value 是字符串数组，缺失即契约漂移。
func (p runtimeParser) requiredStringArray(field string, value any) ([]string, error) {
	return p.stringArray(field, value, false)
}

func (p runtimeParser) stringArray(field string, value any, absentOK bool) ([]string, error) {
	raw, err := p.array(field, value, absentOK)
	if err != nil {
		return nil, err
	}
	items := make([]string, 0, len(raw))
	for i, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, p.fail(fmt.Sprintf("%s[%d]", field, i), fmt.Sprintf("应为字符串，实际为 %T", item))
		}
		items = append(items, text)
	}
	return items, nil
}

// optionalStringMap 在字段缺失或为 null 时返回空 map；值必须是字符串。
// 旧实现会静默丢弃非字符串值，这里改为显式报错，避免渠道声明漂移被吞掉。
func (p runtimeParser) optionalStringMap(field string, value any) (map[string]string, error) {
	raw, err := p.object(field, value, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for key, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, p.fail(field+"."+key, fmt.Sprintf("应为字符串，实际为 %T", item))
		}
		out[key] = text
	}
	return out, nil
}

// optionalEnum 在字段缺失或为 null 时返回空串；存在时必须是允许的枚举值。
func (p runtimeParser) optionalEnum(field string, value any, allowed []string) (string, error) {
	text, err := p.optionalString(field, value)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", nil
	}
	for _, candidate := range allowed {
		if text == candidate {
			return text, nil
		}
	}
	return "", p.fail(field, fmt.Sprintf("非法枚举值 %q（允许: %s）", text, strings.Join(allowed, ", ")))
}

// requiredEnum 断言 value 是必填且合法的枚举值；空串也算非法。
func (p runtimeParser) requiredEnum(field string, value any, allowed []string) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", p.fail(field, fmt.Sprintf("应为字符串，实际为 %T", value))
	}
	for _, candidate := range allowed {
		if text == candidate {
			return text, nil
		}
	}
	return "", p.fail(field, fmt.Sprintf("非法枚举值 %q（允许: %s）", text, strings.Join(allowed, ", ")))
}

// presentString 断言字段存在且是字符串，允许空串。用于“必有但可能为空”的字段，例如空文件的内容。
func (p runtimeParser) presentString(field string, value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case nil:
		return "", p.fail(field, "缺少必填字段")
	default:
		return "", p.fail(field, fmt.Sprintf("应为字符串，实际为 %T", typed))
	}
}

// fieldIndex 把数组下标拼进字段路径，如 "tasks" + 3 → "tasks[3]"。
func fieldIndex(field string, index int) string {
	return field + "[" + strconv.Itoa(index) + "]"
}
