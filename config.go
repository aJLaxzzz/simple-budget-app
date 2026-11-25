package main

import (
	"budget/internal/storageguard"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	minPasswordCharacters = 12
	maxPasswordBytes      = 4096
)

func validPasswordLength(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) >= minPasswordCharacters && len(value) <= maxPasswordBytes
}

type serverConfig struct {
	Addr, DataDir, StaticDir, Password, CertFile, KeyFile string
	BackupDir                                             string
	AllowedHosts                                          map[string]bool
	Local                                                 bool
	Environment, PublicOrigin, ProxyMode, InstallationID  string
}

// Set by the release builder. Local builds retain the convenient dev default,
// but a distributed release must never infer its operating environment.
var releaseProfile string

func readConfig(getenv func(string) string) (serverConfig, error) {
	c := serverConfig{Addr: getenv("BUDGET_ADDR"), DataDir: getenv("BUDGET_DATA_DIR"), StaticDir: getenv("BUDGET_STATIC_DIR"), Password: getenv("BUDGET_PASSWORD"), CertFile: getenv("BUDGET_TLS_CERT"), KeyFile: getenv("BUDGET_TLS_KEY")}
	c.Environment = getenv("BUDGET_ENV")
	if c.Environment == "" && releaseProfile == "production" {
		return c, errors.New("release requires explicit BUDGET_ENV")
	}
	if c.Environment == "" {
		c.Environment = "development"
	}
	if c.Environment != "development" && c.Environment != "test" && c.Environment != "production" {
		return c, errors.New("BUDGET_ENV must be development, test or production")
	}
	c.PublicOrigin = getenv("BUDGET_PUBLIC_ORIGIN")
	c.ProxyMode = getenv("BUDGET_PROXY_MODE")
	c.InstallationID = getenv("BUDGET_INSTALLATION_ID")
	c.BackupDir = getenv("BUDGET_BACKUP_DIR")
	passwordFile := getenv("BUDGET_PASSWORD_FILE")
	systemdCredential := false
	if passwordFile == "" && getenv("CREDENTIALS_DIRECTORY") != "" {
		passwordFile = filepath.Join(getenv("CREDENTIALS_DIRECTORY"), "app-password")
		systemdCredential = true
	}
	if passwordFile != "" {
		if c.Password != "" {
			return c, errors.New("choose password file or password environment, not both")
		}
		var err error
		if systemdCredential {
			c.Password, err = readSystemdPasswordFile(passwordFile)
		} else {
			c.Password, err = readPasswordFile(passwordFile)
		}
		if err != nil {
			return c, err
		}
	}
	if c.Environment == "production" && passwordFile == "" {
		return c, errors.New("production requires BUDGET_PASSWORD_FILE or systemd app-password credential")
	}
	if c.StaticDir != "" && getenv("BUDGET_ENV") != "development" {
		return c, errors.New("external static requires explicit BUDGET_ENV=development")
	}
	if c.Addr == "" {
		c.Addr = "127.0.0.1:3001"
	}
	host, port, e := net.SplitHostPort(c.Addr)
	if e != nil {
		return c, fmt.Errorf("BUDGET_ADDR: expected IP:port: %w", e)
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return c, errors.New("BUDGET_ADDR: port must be 1..65535")
	}
	if host == "localhost" {
		host = "127.0.0.1"
		c.Addr = net.JoinHostPort(host, port)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return c, errors.New("BUDGET_ADDR: use a literal IP address")
	}
	c.Local = ip.IsLoopback()
	if c.Password != "" && !validPasswordLength(c.Password) {
		return c, errors.New("BUDGET_PASSWORD: use at least 12 characters (16 recommended), at most 4096 UTF-8 bytes")
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return c, errors.New("both BUDGET_TLS_CERT and BUDGET_TLS_KEY are required")
	}
	if !c.Local && (c.Password == "" || c.CertFile == "") {
		return c, errors.New("external binding requires BUDGET_PASSWORD and TLS certificate/key")
	}
	c.AllowedHosts = map[string]bool{}
	if raw := getenv("BUDGET_ALLOWED_HOSTS"); raw != "" {
		for _, h := range strings.Split(raw, ",") {
			h = strings.ToLower(strings.TrimSpace(h))
			u, e := url.Parse("https://" + h)
			if e != nil || u.Host != h || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" || strings.ContainsAny(h, "*\\ \t\r\n") {
				return c, errors.New("BUDGET_ALLOWED_HOSTS: expected exact host[:port], no wildcards")
			}
			c.AllowedHosts[h] = true
		}
	} else {
		if ip.IsUnspecified() {
			return c, errors.New("wildcard binding requires BUDGET_ALLOWED_HOSTS")
		}
		c.AllowedHosts[strings.ToLower(c.Addr)] = true
		if c.Local {
			c.AllowedHosts[net.JoinHostPort("localhost", port)] = true
		}
	}
	if c.DataDir == "" {
		c.DataDir = "."
	}
	if c.Environment == "production" && !filepath.IsAbs(c.DataDir) {
		return c, errors.New("production requires absolute BUDGET_DATA_DIR")
	}
	c.DataDir, e = filepath.Abs(c.DataDir)
	if e != nil {
		return c, e
	}
	if c.StaticDir != "" {
		c.StaticDir, e = filepath.Abs(c.StaticDir)
		if e != nil {
			return c, e
		}
	}
	info, e := os.Stat(c.DataDir)
	if e != nil || !info.IsDir() {
		return c, fmt.Errorf("BUDGET_DATA_DIR must be an existing directory: %s", c.DataDir)
	}
	if c.ProxyMode != "" && c.ProxyMode != "loopback-https" {
		return c, errors.New("unsupported BUDGET_PROXY_MODE")
	}
	if c.ProxyMode != "" {
		u, err := url.Parse(c.PublicOrigin)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(u.Host, "*\\ \t\r\n") || !c.Local || c.CertFile != "" || c.Password == "" {
			return c, errors.New("loopback-https requires canonical HTTPS origin, loopback, password and no native TLS")
		}
		if len(c.AllowedHosts) != 1 || !c.AllowedHosts[strings.ToLower(u.Host)] || getenv("BUDGET_ALLOWED_HOSTS") == "" {
			return c, errors.New("Host allowlist must contain exactly the canonical origin host")
		}
		c.PublicOrigin = "https://" + strings.ToLower(u.Host)
	} else if c.PublicOrigin != "" {
		return c, errors.New("PUBLIC_ORIGIN requires explicit proxy mode")
	}
	if c.Environment == "production" {
		if c.ProxyMode != "loopback-https" || c.InstallationID == "" || getenv("BUDGET_DATA_DIR") == "" || getenv("BUDGET_MIGRATIONS") != "check-only" {
			return c, errors.New("production requires proxy mode, installation ID, data path and BUDGET_MIGRATIONS=check-only")
		}
	}
	real, err := storageguard.Check(c.DataDir, c.Environment, c.InstallationID)
	if err != nil {
		return c, err
	}
	c.DataDir = real
	if c.Environment == "production" {
		info, err := os.Stat(real)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			return c, errors.New("production directory must have owner-only permissions")
		}
		for _, name := range []string{"installation.json", "planner.db", "data.json", "categories.json"} {
			if e = storageguard.Regular(filepath.Join(real, name), true); e != nil {
				return c, fmt.Errorf("production %s: %w", name, e)
			}
		}
	}
	if c.BackupDir != "" {
		if c.InstallationID == "" {
			m, err := storageguard.ReadMarker(c.DataDir)
			if err != nil {
				return c, err
			}
			c.InstallationID = m.ID
		}
		r, err := openRepository(c.BackupDir, c.InstallationID, c.Environment, false)
		if err != nil {
			return c, err
		}
		c.BackupDir = r.Dir

		for _, pair := range [][2]string{{c.DataDir, c.BackupDir}, {c.BackupDir, c.DataDir}} {
			rel, _ := filepath.Rel(pair[0], pair[1])
			if rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
				return c, errors.New("backup and live directories must be separate")
			}
		}
	}
	return c, nil
}

func readPasswordFile(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("password file must be absolute")
	}
	if e := storageguard.Regular(path, true); e != nil {
		return "", errors.New("password file must be private, regular and not a link")
	}
	info, e := os.Stat(path)
	if e != nil || info.Size() > maxPasswordBytes+2 {
		return "", errors.New("invalid password file size")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return "", errors.New("cannot read password file")
	}
	return passwordValue(raw)
}

func passwordValue(raw []byte) (string, error) {
	value := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if !validPasswordLength(value) || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("password file requires one line of at least 12 characters (16 recommended), at most 4096 UTF-8 bytes")
	}
	return value, nil
}
