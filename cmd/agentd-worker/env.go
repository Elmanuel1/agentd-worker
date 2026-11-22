package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// env reads settings, collecting every problem so start-up reports them all at once.
type env struct {
	errs []string
}

func (e *env) str(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (e *env) required(key string) string {
	v := os.Getenv(key)
	if v == "" {
		e.errs = append(e.errs, key+" is required")
	}
	return v
}

func (e *env) list(key string) []string {
	var out []string
	for v := range strings.SplitSeq(e.required(key), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (e *env) seconds(key string, def int) time.Duration {
	return time.Duration(e.number(key, def, 1)) * time.Second
}

func (e *env) number(key string, def, least int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < least {
		e.errs = append(e.errs, fmt.Sprintf("%s must be a whole number of at least %d", key, least))
	}
	return n
}

func (e *env) duration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		e.errs = append(e.errs, key+" must be a positive duration")
	}
	return d
}

func (e *env) err() error {
	if len(e.errs) == 0 {
		return nil
	}
	return fmt.Errorf("settings: %s", strings.Join(e.errs, "; "))
}
