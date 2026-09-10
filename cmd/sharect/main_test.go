package main

import (
	"log/slog"
	"testing"
)

func TestHealthcheckURL(t *testing.T) {
	cases := []struct {
		listen, want string
	}{
		{"0.0.0.0:8080", "http://127.0.0.1:8080/healthz"},
		{":8080", "http://127.0.0.1:8080/healthz"},
		{"[::]:8080", "http://[::1]:8080/healthz"},
		{"127.0.0.1:9000", "http://127.0.0.1:9000/healthz"},
	}
	for _, c := range cases {
		got, err := healthcheckURL(c.listen)
		if err != nil {
			t.Fatalf("%s: %v", c.listen, err)
		}
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.listen, got, c.want)
		}
	}
	if _, err := healthcheckURL("8080"); err == nil {
		t.Error("a bare port is not a host:port and must be rejected")
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warn": slog.LevelWarn, "Error": slog.LevelError,
	} {
		got, err := parseLevel(in)
		if err != nil || got != want {
			t.Errorf("parseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if got, err := parseLevel("loud"); err == nil || got != slog.LevelInfo {
		t.Errorf("parseLevel(loud) = %v, %v; want info + error", got, err)
	}
}
