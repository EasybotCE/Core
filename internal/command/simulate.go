// Package command implements side-effect-free command evaluation.
package command

import (
	"regexp"
	"strconv"
	"strings"

	"easybot/internal/store"
)

// Context is hypothetical in simulation. A future runtime must derive it from
// trusted platform/binding services, never directly from a message payload.
type Context struct {
	UserID     string   `json:"userId"`
	SenderName string   `json:"senderName"`
	Verified   bool     `json:"verified"`
	Role       string   `json:"role"`
	Bound      bool     `json:"bound"`
	Identities []string `json:"identities"`
}
type Result struct {
	Simulated        bool     `json:"simulated"`
	Executed         bool     `json:"executed"`
	RuntimeAvailable bool     `json:"runtimeAvailable"`
	Matched          bool     `json:"matched"`
	Allowed          bool     `json:"allowed"`
	Reason           string   `json:"reason"`
	Trigger          string   `json:"trigger,omitempty"`
	Arguments        []string `json:"arguments"`
	Reply            string   `json:"reply,omitempty"`
}

func ValidateInput(message string, ctx Context) error {
	if len(message) > 8192 || len(ctx.UserID) > 128 || len(ctx.SenderName) > 512 || len(ctx.Identities) > 32 {
		return &store.ValidationError{Field: "context", Message: "消息或模拟身份超过长度上限"}
	}
	for _, identity := range ctx.Identities {
		if len(identity) > 120 {
			return &store.ValidationError{Field: "context.identities", Message: "模拟身份标识最多 120 字节"}
		}
	}
	return nil
}

// Simulate validates configuration even for disabled commands, then evaluates
// one command only. No database, network, process or cooldown state is changed.
func Simulate(raw string, enabled bool, message string, ctx Context) (Result, error) {
	result := Result{Simulated: true, Arguments: []string{}}
	config, err := store.ValidateCommandConfig(raw)
	if err != nil {
		return result, err
	}
	if err := ValidateInput(message, ctx); err != nil {
		return result, err
	}
	if !enabled {
		result.Reason = "disabled"
		return result, nil
	}
	result.Reason = "not_matched"
	if !strings.HasPrefix(message, config.Prefix) {
		return result, nil
	}
	body := strings.TrimPrefix(message, config.Prefix)
	for _, trigger := range config.Triggers {
		if config.Match == "exact" {
			if body != trigger {
				continue
			}
		} else {
			re := regexp.MustCompile("\\A(?:" + trigger + ")\\z") // validated above; bounded RE2 patterns
			parts := re.FindStringSubmatch(body)
			if parts == nil {
				continue
			}
			result.Arguments = parts[1:]
		}
		result.Matched = true
		result.Trigger = trigger
		break
	}
	if !result.Matched {
		return result, nil
	}
	result.Allowed = config.Permission == "all"
	if !result.Allowed && ctx.Verified && ctx.UserID != "" {
		switch config.Permission {
		case "admin":
			result.Allowed = ctx.Role == "admin" || ctx.Role == "owner"
		case "owner":
			result.Allowed = ctx.Role == "owner"
		case "bound":
			result.Allowed = ctx.Bound
		case "custom":
			for _, identity := range ctx.Identities {
				if identity == config.Identity {
					result.Allowed = true
					break
				}
			}
		}
	}
	if !result.Allowed {
		result.Reason = "permission_denied"
		return result, nil
	}
	values := []string{"{sender_name}", ctx.SenderName, "{user_id}", ctx.UserID}
	for i := 1; i <= 9; i++ {
		value := ""
		if i <= len(result.Arguments) {
			value = result.Arguments[i-1]
		}
		values = append(values, "{arg"+strconv.Itoa(i)+"}", value)
	}
	// One pass prevents user input containing placeholders from being re-expanded.
	result.Reply = strings.NewReplacer(values...).Replace(config.Reply)
	if len(result.Reply) > 16000 {
		result.Allowed = false
		result.Reply = ""
		result.Reason = "reply_too_long"
		return result, nil
	}
	result.Reason = "preview_ready"
	return result, nil
}
