package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/kusold/canopy/internal/canopy"
	"github.com/kusold/grove"
)

func TestMainBuilds(t *testing.T) {
	// Verify the main package compiles. If this test runs at all, main.go
	// compiled successfully.
}

func TestMainGracefulShutdown(t *testing.T) {
	t.Run("SIGINT triggers graceful shutdown", func(t *testing.T) {
		addr := allocateAddr(t)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		runDone := make(chan error, 1)
		go func() {
			runDone <- grove.Run(ctx, canopy.Module{}, grove.WithHTTP())
		}()

		waitForHTTP(t, addr, "/healthz")

		// Verify the server is responding before shutdown
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://" + addr + "/healthz")
		if err != nil {
			t.Fatalf("server not responding on %s: %v", addr, err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("close healthz response body: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		// Send SIGINT
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(syscall.SIGINT)

		select {
		case err := <-runDone:
			if err != nil {
				t.Fatalf("Run() returned unexpected error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run() did not complete within timeout after SIGINT")
		}
	})

	t.Run("SIGTERM triggers graceful shutdown", func(t *testing.T) {
		addr := allocateAddr(t)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		runDone := make(chan error, 1)
		go func() {
			runDone <- grove.Run(ctx, canopy.Module{}, grove.WithHTTP())
		}()

		waitForHTTP(t, addr, "/healthz")

		// Verify server responds
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://" + addr + "/healthz")
		if err != nil {
			t.Fatalf("server not responding on %s: %v", addr, err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("close healthz response body: %v", err)
		}

		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(syscall.SIGTERM)

		select {
		case err := <-runDone:
			if err != nil {
				t.Fatalf("Run() returned unexpected error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run() did not complete within timeout after SIGTERM")
		}
	})
}

func TestMainServerEndpoints(t *testing.T) {
	t.Run("GET /healthz returns 200 through running server", func(t *testing.T) {
		addr := allocateAddr(t)
		runServer(t, addr)

		resp := mustGet(t, "http://"+addr+"/healthz")
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode healthz response: %v", err)
		}
		if body["status"] != "ok" {
			t.Errorf("healthz status = %v, want %q", body["status"], "ok")
		}
	})

	t.Run("GET /readyz returns 200 through running server", func(t *testing.T) {
		addr := allocateAddr(t)
		runServer(t, addr)

		resp := mustGet(t, "http://"+addr+"/readyz")
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("readyz status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode readyz response: %v", err)
		}
		if body["status"] != "ok" {
			t.Errorf("readyz status = %v, want %q", body["status"], "ok")
		}
	})

	t.Run("GET /example/hello returns expected JSON through running server", func(t *testing.T) {
		addr := allocateAddr(t)
		runServer(t, addr)

		resp := mustGet(t, "http://"+addr+"/example/hello")
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/example/hello status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		ct := resp.Header.Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		var body map[string]string
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			t.Fatalf("decode response: %v, body: %s", err, bodyBytes)
		}
		if body["message"] != "hello from canopy" {
			t.Errorf("message = %q, want %q", body["message"], "hello from canopy")
		}
	})
}

func TestMainServerTenantEndpoints(t *testing.T) {
	t.Run("GET /example/whoami-tenant returns tenant info with headers", func(t *testing.T) {
		addr := allocateAddr(t)
		runServer(t, addr)

		req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/example/whoami-tenant", nil)
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		req.Header.Set("X-Tenant-ID", "00000000-0000-0000-0000-000000000001")
		req.Header.Set("X-Tenant-Slug", "acme")

		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /example/whoami-tenant: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, bodyBytes)
		}

		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body["tenant_id"] != "00000000-0000-0000-0000-000000000001" {
			t.Errorf("tenant_id = %q, want %q", body["tenant_id"], "00000000-0000-0000-0000-000000000001")
		}
		if body["tenant_slug"] != "acme" {
			t.Errorf("tenant_slug = %q, want %q", body["tenant_slug"], "acme")
		}
	})

	t.Run("GET /example/whoami-tenant returns 422 without tenant headers", func(t *testing.T) {
		addr := allocateAddr(t)
		runServer(t, addr)

		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://" + addr + "/example/whoami-tenant")
		if err != nil {
			t.Fatalf("GET /example/whoami-tenant: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
		}
	})
}

// allocateAddr finds a free TCP port, sets HTTP_ADDR to it, and returns the
// address string. The port is released before returning so the server can bind
// to it.
func allocateAddr(t *testing.T) string {
	t.Helper()
	clearConfigEnv(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate listener: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	t.Setenv("HTTP_ADDR", addr)
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "5s")
	return addr
}

// runServer starts grove.Run with the Canopy module in a goroutine, waits for
// it to become available, and registers cleanup to cancel the context.
func runServer(t *testing.T, addr string) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- grove.Run(ctx, canopy.Module{}, grove.WithHTTP(), grove.WithTenancy())
	}()

	waitForHTTP(t, addr, "/healthz")

	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(10 * time.Second):
			t.Log("warning: server did not shut down within timeout")
		}
	})
}

// mustGet performs an HTTP GET and fails the test on error.
func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SERVICE_NAME", "SERVICE_ENV", "SERVICE_VERSION", "HTTP_ADDR", "LOG_FORMAT", "LOG_COLOR", "HTTP_SHUTDOWN_TIMEOUT"} {
		t.Setenv(key, "")
	}
}

func waitForHTTP(t *testing.T, addr, path string) {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + path)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server did not serve %s: %v", path, lastErr)
}
