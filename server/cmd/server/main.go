package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"

	"mobiledev/server/internal/auth"
	"mobiledev/server/internal/cert"
	"mobiledev/server/internal/config"
	"mobiledev/server/internal/hub"
	"mobiledev/server/internal/ws"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}

	certificate, generated, err := cert.Ensure(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("prepare TLS certificate: %w", err)
	}
	if generated {
		log.Printf("generated self-signed TLS certificate: cert=%s key=%s", cfg.CertFile, cfg.KeyFile)
	} else {
		log.Printf("loaded existing TLS certificate: cert=%s key=%s", cfg.CertFile, cfg.KeyFile)
	}

	authenticator := auth.New(cfg.Password)
	connectionHub := hub.New()
	wsServer := ws.New(authenticator, connectionHub)

	mux := http.NewServeMux()
	mux.Handle("/health", authenticator.Middleware(http.HandlerFunc(healthHandler)))
	mux.Handle("/api/agents", authenticator.Middleware(agentsHandler(connectionHub)))
	mux.HandleFunc("/ws/agent", wsServer.HandleAgent)
	mux.HandleFunc("/ws/client", wsServer.HandleClient)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: mux,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		},
	}

	log.Printf("server listening on https://0.0.0.0:%d", cfg.Port)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func agentsHandler(connectionHub *hub.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(connectionHub.Agents())
	}
}
