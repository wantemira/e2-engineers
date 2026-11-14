package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-buddy-training/orders/internal/httpapi"
	"ai-buddy-training/orders/internal/orders"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	address := os.Getenv("HTTP_ADDR")
	if address == "" {
		address = "127.0.0.1:8081"
	}
	server := &http.Server{
		Addr: address, Handler: httpapi.New(orders.NewService(&orders.MemoryRepository{})),
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- server.ListenAndServe() }()
	log.Printf("orders-training listening on %s", address)
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := server.Shutdown(shutdown); err != nil {
			return err
		}
		err := <-served
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
