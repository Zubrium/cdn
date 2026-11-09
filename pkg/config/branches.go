package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// BranchConfig is operator-owned. IDs and snapshot assignments live in the DB.
type BranchConfig struct {
	Name        string   `json:"name"`
	HTTPAddr    string   `json:"httpAddr"`
	CORSOrigins []string `json:"corsOrigins"`
	Indexable   *bool    `json:"indexable"`
}

func ParseBranches(raw, adminAddr string) ([]BranchConfig, error) {
	var branches []BranchConfig
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&branches); err != nil {
		return nil, fmt.Errorf("BRANCHES: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("BRANCHES must contain exactly one JSON array")
	}
	if len(branches) == 0 {
		return nil, fmt.Errorf("BRANCHES must contain at least one branch")
	}
	names := map[string]bool{}
	ports := map[int]bool{}
	portOf := func(addr string) (int, error) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			// net.AddrError includes the supplied address in Error(), which may
			// contain credentials when an operator accidentally configures a URL.
			return 0, fmt.Errorf("address must be a valid host:port pair")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return 0, fmt.Errorf("port must be between 1 and 65535")
		}
		return n, nil
	}
	adminPort, err := portOf(adminAddr)
	if err != nil {
		return nil, fmt.Errorf("HTTP_ADDR: %w", err)
	}
	ports[adminPort] = true
	for i := range branches {
		b := &branches[i]
		b.Name = strings.TrimSpace(b.Name)
		if b.Name == "" || len([]rune(b.Name)) > 100 || names[b.Name] {
			return nil, fmt.Errorf("BRANCHES: names must be unique and contain 1–100 characters")
		}
		names[b.Name] = true
		port, err := portOf(b.HTTPAddr)
		if err != nil || ports[port] {
			return nil, fmt.Errorf("BRANCHES %q: invalid or duplicate port (including admin port)", b.Name)
		}
		ports[port] = true
		if b.Indexable == nil || b.CORSOrigins == nil {
			return nil, fmt.Errorf("BRANCHES %q: indexable and corsOrigins must be explicit", b.Name)
		}
		for _, origin := range b.CORSOrigins {
			if origin == "*" {
				continue
			}
			u, e := url.Parse(origin)
			if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				// Do not echo origin input: a malformed origin can contain
				// userinfo and leak credentials into startup logs.
				return nil, fmt.Errorf("BRANCHES entry %q: invalid CORS origin", b.Name)
			}
		}
	}
	return branches, nil
}
