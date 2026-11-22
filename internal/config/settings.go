package config

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
)

type fileConfig struct {
	Server struct {
		Address      string `json:"address"`
		AllowedHosts string `json:"allowedHosts"`
		TLS          struct {
			Certificate string `json:"certificate"`
			PrivateKey  string `json:"privateKey"`
		} `json:"tls"`
	} `json:"server"`
	Bridge struct {
		Address string `json:"address"`
		TLS     struct {
			Certificate string `json:"certificate"`
			PrivateKey  string `json:"privateKey"`
		} `json:"tls"`
	} `json:"bridge"`
	Database struct {
		URL        string `json:"url"`
		SQLitePath string `json:"sqlitePath"`
	} `json:"database"`
	Auth struct {
		Password string `json:"password"`
	} `json:"auth"`
	Logging struct {
		Level string `json:"level"`
	} `json:"logging"`
}

type Settings struct {
	Path, Address, Certificate, PrivateKey, Database, SQLitePath, AllowedHosts string
	BridgeAddress, Password                                                    string
	BridgeCertificate, BridgePrivateKey                                        string
	LogLevel                                                                   slog.Level
}

func defaults() fileConfig {
	var value fileConfig
	value.Server.Address = "0.0.0.0:5001"
	value.Server.AllowedHosts = "*"
	value.Bridge.Address = "0.0.0.0:26991"
	value.Database.SQLitePath = "appdata/EasybotCE.db"
	value.Logging.Level = "debug"
	return value
}

// BaseDirectory always refers to the executable, never to the shell's working
// directory. IDE/go run builds therefore use their temporary binary directory;
// developers can opt into a persistent location with EASYBOT_CONFIG.
func BaseDirectory() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(executable), nil
}

func Load() (Settings, bool, error) {
	dir, err := BaseDirectory()
	if err != nil {
		return Settings{}, false, err
	}
	return LoadFile(configPath(dir, os.Getenv("EASYBOT_CONFIG")))
}

func configPath(executableDirectory, configured string) string {
	if configured == "" {
		return filepath.Join(executableDirectory, "config.json")
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(executableDirectory, configured)
}

// LoadFile loads the Go-native config.json format. On first launch it creates
// the file and stores a randomly generated 12-digit administrator password.
func LoadFile(path string) (Settings, bool, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Settings{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Settings{}, false, err
	}
	created := false
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		value := defaults()
		value.Auth.Password, err = newDefaultPassword()
		if err != nil {
			return Settings{}, false, err
		}
		data, err = json.MarshalIndent(value, "", "  ")
		if err != nil {
			return Settings{}, false, err
		}
		data = append(data, '\n')
		if err = writeConfig(path, data); err != nil {
			return Settings{}, false, err
		}
		created = true
	} else if err != nil {
		return Settings{}, false, err
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return Settings{}, created, fmt.Errorf("config.json 格式错误: %w", err)
	}
	for _, legacy := range []string{"Kestrel", "Serilog", "ServerOptions", "AllowedHosts"} {
		if _, exists := keys[legacy]; exists {
			return Settings{}, created, fmt.Errorf("检测到旧版 .NET 配置字段 %q；请迁移到 config.json 的 server/bridge/database/auth/logging 结构", legacy)
		}
	}
	value := defaults()
	if err := json.Unmarshal(data, &value); err != nil {
		return Settings{}, created, fmt.Errorf("config.json 格式错误: %w", err)
	}
	generatedPassword := false
	if strings.TrimSpace(value.Auth.Password) == "" {
		value.Auth.Password, err = newDefaultPassword()
		if err != nil {
			return Settings{}, created, err
		}
		generatedPassword = true
		encoded, marshalErr := json.MarshalIndent(value, "", "  ")
		if marshalErr != nil {
			return Settings{}, created, marshalErr
		}
		if err := writeConfig(path, append(encoded, '\n')); err != nil {
			return Settings{}, created, err
		}
	}
	address, err := normalizeAddress(value.Server.Address, "管理服务")
	if err != nil {
		return Settings{}, created || generatedPassword, err
	}
	bridgeAddress, err := normalizeAddress(value.Bridge.Address, "Bridge")
	if err != nil {
		return Settings{}, created || generatedPassword, err
	}
	base := filepath.Dir(path)
	resolve := func(name string) string {
		if name == "" || filepath.IsAbs(name) {
			return name
		}
		return filepath.Join(base, name)
	}
	certificate, privateKey := resolve(value.Server.TLS.Certificate), resolve(value.Server.TLS.PrivateKey)
	if err := ValidateListener(address, certificate, privateKey); err != nil {
		return Settings{}, created || generatedPassword, err
	}
	bridgeCertificate, bridgePrivateKey := resolve(value.Bridge.TLS.Certificate), resolve(value.Bridge.TLS.PrivateKey)
	if err := ValidateListener(bridgeAddress, bridgeCertificate, bridgePrivateKey); err != nil {
		return Settings{}, created || generatedPassword, fmt.Errorf("Bridge TLS: %w", err)
	}
	level, err := parseLogLevel(value.Logging.Level)
	if err != nil {
		return Settings{}, created || generatedPassword, err
	}
	database, sqlitePath := value.Database.URL, resolve(value.Database.SQLitePath)
	for name, target := range map[string]*string{
		"EASYBOT_ADDR": &address, "EASYBOT_DATABASE": &database,
		"EASYBOT_SQLITE_PATH": &sqlitePath, "EASYBOT_TLS_CERT": &certificate,
		"EASYBOT_TLS_KEY": &privateKey, "EASYBOT_ALLOWED_HOSTS": &value.Server.AllowedHosts,
	} {
		if env, ok := os.LookupEnv(name); ok {
			*target = env
		}
	}
	sqlitePath, certificate, privateKey = resolve(sqlitePath), resolve(certificate), resolve(privateKey)
	if err := ValidateListener(address, certificate, privateKey); err != nil {
		return Settings{}, created || generatedPassword, err
	}
	return Settings{
		Path: path, Address: address, BridgeAddress: bridgeAddress,
		Certificate: certificate, PrivateKey: privateKey,
		BridgeCertificate: bridgeCertificate, BridgePrivateKey: bridgePrivateKey,
		Database: database, SQLitePath: sqlitePath, AllowedHosts: value.Server.AllowedHosts,
		Password: value.Auth.Password, LogLevel: level,
	}, created || generatedPassword, nil
}

func normalizeAddress(address, name string) (string, error) {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return "", fmt.Errorf("%s address must be host:port: %w", name, err)
	}
	return address, nil
}

func writeConfig(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	// The portable package contains an existing, normally 0644 template.
	// Restrict that file before writing a generated plaintext password; the
	// mode passed to OpenFile only applies when creating a new file.
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if err := file.Truncate(0); err != nil {
		file.Close()
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func newDefaultPassword() (string, error) {
	var password strings.Builder
	for i := 0; i < 12; i++ {
		digit, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		password.WriteByte(byte('0' + digit.Int64()))
	}
	return password.String(), nil
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "debug":
		return slog.LevelDebug, nil
	case "info", "information":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported logging.level %q", value)
	}
}

func AllowedHost(host, patterns string) bool {
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if patterns == "" {
		return true
	}
	for _, pattern := range strings.Split(strings.ToLower(patterns), ";") {
		pattern = strings.Trim(strings.TrimSpace(pattern), "[]")
		if pattern == "*" || host == pattern || (strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:])) {
			return true
		}
	}
	return false
}
