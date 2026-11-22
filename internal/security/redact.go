// Package security contains small security policies shared by infrastructure packages.
package security

import (
	"regexp"
)

var (
	SensitiveLogKey  = regexp.MustCompile(`(?i)(token|password|passwd|secret|api[_-]?key|authorization|dsn|challenge|verification[_-]?code)$`)
	secretAssignment = regexp.MustCompile(`(?i)(token|password|passwd|secret|api[_-]?key|authorization|dsn|challenge|verification[_-]?code)(\s*[=:]\s*)([^\s&,;]+)`)
	bearerToken      = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/-]+=*`)
	urlCredentials   = regexp.MustCompile(`(?i)(://[^:/\s]+:)[^@/\s]+@`)
)

func Redact(value string) string {
	value = bearerToken.ReplaceAllString(value, "Bearer [REDACTED]")
	value = urlCredentials.ReplaceAllString(value, "$1[REDACTED]@")
	return secretAssignment.ReplaceAllStringFunc(value, func(match string) string {
		parts := secretAssignment.FindStringSubmatch(match)
		if len(parts) != 4 {
			return "[REDACTED]"
		}
		return parts[1] + parts[2] + "[REDACTED]"
	})
}
