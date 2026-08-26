package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sqc157400661/kdb/internal/alertrelay"
)

func main() {
	listen := flag.String("listen", ":8080", "relay listen address")
	flag.Parse()
	fallback, err := alertrelay.LoadFallbackConfig(os.Getenv("KDB_ALERT_FALLBACK_CONFIG"))
	if err != nil {
		log.Fatal(err)
	}
	config := alertrelay.Config{CellID: os.Getenv("KDB_ALERT_CELL_ID"), RelayID: os.Getenv("KDB_ALERT_RELAY_ID"), Version: os.Getenv("KDB_ALERT_RELAY_VERSION"), CenterEndpoint: os.Getenv("KDB_ALERT_CENTER_ENDPOINT"), TLSCertificate: "/var/run/kdb-alert-relay/tls/tls.crt", TLSPrivateKey: "/var/run/kdb-alert-relay/tls/tls.key", TLSCA: "/var/run/kdb-alert-relay/tls/ca.crt", Fallback: fallback}
	relay, err := alertrelay.New(config)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *listen, Handler: relay.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if err := relay.SendHeartbeat(ctx); err != nil {
				log.Printf("relay heartbeat failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
