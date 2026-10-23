package server

import (
	"net"
	"net/url"
	"strings"
)

func allowedHost(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\/\r\n\t ") {
		return false
	}
	host := value
	if strings.HasPrefix(value, "[") {
		end := strings.IndexByte(value, ']')
		if end < 0 {
			return false
		}
		host = value[1:end]
		rest := value[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") || !validPort(rest[1:]) {
				return false
			}
		}
	} else if strings.Contains(value, ":") {
		var err error
		host, _, err = net.SplitHostPort(value)
		if err != nil {
			return false
		}
		_, port, _ := net.SplitHostPort(value)
		if !validPort(port) {
			return false
		}
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validPort(port string) bool {
	if port == "" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	var value int
	for _, r := range port {
		value = value*10 + int(r-'0')
		if value > 65535 {
			return false
		}
	}
	return value > 0
}

func allowedOrigin(value, requestHost string) bool {
	if value == "" {
		return true
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme != "http" {
		return false
	}
	return strings.EqualFold(u.Host, requestHost)
}
