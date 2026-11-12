package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/buddy"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	rootFlag := flag.String("root", "..", "project root containing teams, skills and mocks")
	flag.Parse()
	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	if _, err := buddy.LoadTeam(root); err != nil {
		return err
	}
	address := os.Getenv("HTTP_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(root, ".data")
	}
	store, err := buddy.NewStore(filepath.Join(dataDir, "application.json"))
	if err != nil {
		return err
	}
	secret := rand.Text()
	gitHandler, err := buddy.NewGitHandler(filepath.Join(root, "mocks", "pr-17.json"), filepath.Join(dataDir, "git.json"), secret)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	defer listener.Close()
	endpoint := *listener.Addr().(*net.TCPAddr)
	if endpoint.IP.IsUnspecified() {
		if endpoint.IP.To4() != nil {
			endpoint.IP = net.IPv4(127, 0, 0, 1)
		} else {
			endpoint.IP = net.IPv6loopback
		}
	}
	git := buddy.NewMCPGit("http://"+endpoint.String()+"/internal/mcp", secret)
	service := buddy.NewService(store, git, buddy.NewLocalAssistant(), root)
	origins := []string{"http://127.0.0.1:5173", "http://localhost:5173"}
	if configured := os.Getenv("FRONTEND_ORIGIN"); configured != "" {
		origins = []string{configured}
	}
	server := &http.Server{Addr: address, Handler: buddy.NewHTTPHandler(service, gitHandler, origins),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := make(chan error, 1)
	go func() { workerDone <- service.Run(ctx) }()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	slog.Info("AI-Buddy API started", "address", listener.Addr().String(), "assistant", "local", "storage", dataDir)
	var runErr error
	workerExited := false
	select {
	case <-ctx.Done():
	case runErr = <-workerDone:
		workerExited = true
		stop()
	case runErr = <-serverDone:
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	if !workerExited {
		select {
		case err := <-workerDone:
			if runErr == nil {
				runErr = err
			}
		case <-shutdown.Done():
			return shutdown.Err()
		}
	}
	if errors.Is(runErr, http.ErrServerClosed) {
		return nil
	}
	return runErr
}
