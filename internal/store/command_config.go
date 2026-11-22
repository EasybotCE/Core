package store

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string  { return e.Message }
func invalid(field, message string) error { return &ValidationError{Field: field, Message: message} }

var routeVariable = regexp.MustCompile(`\{([^{}]+)\}`)

func decodeContract(raw string, target any) error {
	if len(raw) > 64<<10 {
		return invalid("detail", "配置不能超过 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalid("detail", "配置字段或 JSON 格式无效")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return invalid("detail", "配置必须为单个 JSON 对象")
	}
	return nil
}

// CommandConfig describes text-only commands. It does not grant execution rights.
type CommandConfig struct {
	SchemaVersion int      `json:"schemaVersion"`
	Prefix        string   `json:"prefix"`
	Match         string   `json:"match"`
	Triggers      []string `json:"triggers"`
	Permission    string   `json:"permission"`
	Identity      string   `json:"identity"`
	Action        string   `json:"action"`
	Reply         string   `json:"reply"`
}

func DefaultCommandConfig() CommandConfig {
	return CommandConfig{SchemaVersion: 1, Prefix: "/", Match: "exact", Triggers: []string{"帮助", "help"}, Permission: "all", Action: "text", Reply: "你好，{sender_name}"}
}

func ValidateCommandConfig(raw string) (CommandConfig, error) {
	var config CommandConfig
	if err := decodeContract(raw, &config); err != nil {
		return config, err
	}
	if config.SchemaVersion != 1 {
		return config, invalid("schemaVersion", "命令配置须明确使用 schemaVersion 1；旧配置请核对后更新")
	}
	if len(config.Prefix) > 32 || strings.IndexFunc(config.Prefix, unicode.IsSpace) >= 0 || strings.ContainsRune(config.Prefix, '\x00') {
		return config, invalid("prefix", "前缀最多 32 字节且不能包含空白或 NUL")
	}
	if config.Match != "exact" && config.Match != "regex" {
		return config, invalid("match", "匹配方式仅支持 exact 或 regex")
	}
	if len(config.Triggers) < 1 || len(config.Triggers) > 16 {
		return config, invalid("triggers", "命令需要 1 到 16 个关键词或正则表达式")
	}
	seen := map[string]bool{}
	maxCaptures := 0
	for _, trigger := range config.Triggers {
		if strings.TrimSpace(trigger) == "" || len(trigger) > 512 || seen[trigger] || strings.ContainsRune(trigger, '\x00') {
			return config, invalid("triggers", "关键词或表达式不能为空、重复或超过 512 字节")
		}
		seen[trigger] = true
		if config.Match == "exact" {
			if strings.IndexFunc(trigger, unicode.IsSpace) >= 0 {
				return config, invalid("triggers", "精确关键词不能包含空白；参数请使用正则捕获组")
			}
		} else {
			// Validate the expression independently so it cannot close our wrapper
			// and move an alternative outside the full-message anchors.
			if _, err := regexp.Compile(trigger); err != nil {
				return config, invalid("triggers", "正则表达式无效；使用 Go RE2 语法")
			}
			re, err := regexp.Compile("\\A(?:" + trigger + ")\\z")
			if err != nil || re.NumSubexp() > 9 {
				return config, invalid("triggers", "正则表达式无效或捕获组超过 9 个；使用 Go RE2 语法")
			}
			if re.NumSubexp() > maxCaptures {
				maxCaptures = re.NumSubexp()
			}
		}
	}
	switch config.Permission {
	case "all", "admin", "owner", "bound":
		if config.Identity != "" {
			return config, invalid("identity", "非自定义身份权限不能设置 identity")
		}
	case "custom":
		if strings.TrimSpace(config.Identity) != config.Identity || config.Identity == "" || len(config.Identity) > 120 {
			return config, invalid("identity", "自定义权限需要 1 到 120 字节的身份标识")
		}
	default:
		return config, invalid("permission", "权限必须为 all、admin、owner、bound 或 custom")
	}
	if config.Action != "text" {
		return config, invalid("action", "当前命令合同只支持 text 回复，服务器命令及 PAPI 尚未开放")
	}
	if strings.TrimSpace(config.Reply) == "" || len(config.Reply) > 4000 {
		return config, invalid("reply", "回复必须为 1 到 4000 字节")
	}
	for _, match := range routeVariable.FindAllStringSubmatch(config.Reply, -1) {
		if match[1] == "sender_name" || match[1] == "user_id" {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(match[1], "arg"))
		if !strings.HasPrefix(match[1], "arg") || err != nil || index < 1 || index > maxCaptures || match[1] != "arg"+strconv.Itoa(index) {
			return config, invalid("reply", "回复变量仅允许 sender_name、user_id 和已定义捕获组 arg1 至 arg9")
		}
	}
	if strings.ContainsAny(routeVariable.ReplaceAllString(config.Reply, ""), "{}") {
		return config, invalid("reply", "回复变量括号不完整")
	}
	return config, nil
}
