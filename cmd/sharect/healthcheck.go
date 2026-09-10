package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// healthcheckTimeout stays under the Dockerfile's HEALTHCHECK --timeout=3s.
const healthcheckTimeout = 2 * time.Second

// healthcheckURL turns the listen address into the loopback URL of /healthz: the server
// binds a wildcard address, and a wildcard is not something you can connect to.
func healthcheckURL(listenAddr string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("LISTEN_ADDR %q: %w", listenAddr, err)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}

// runHealthcheck GETs the server's own /healthz and returns the process exit code.
// The runtime image is distroless (no curl), so the binary is its own probe.
func runHealthcheck(listenAddr string) int {
	url, err := healthcheckURL(listenAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return exitFailure
	}
	client := &http.Client{Timeout: healthcheckTimeout}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return exitFailure
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		fmt.Fprintf(os.Stderr, "healthcheck: %s -> %d %q\n", url, resp.StatusCode, body)
		return exitFailure
	}
	return 0
}
