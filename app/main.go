package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

func main() {
	address := flag.String("listen-address", ":8080", "HTTP listen address")
	timeout := flag.Duration("api-timeout", 5*time.Second, "Maximum time for API collection per scrape")
	inCluster := flag.Bool("in-cluster", false, "Use a pod service account instead of local kubeconfig")
	flag.Parse()
	if *timeout <= 0 {
		log.Fatal("api-timeout must be positive")
	}
	config, err := loadConfig(*inCluster)
	if err != nil {
		log.Fatal(err)
	}
	config.Timeout = *timeout
	kube, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(&readinessCollector{kube: kube, dynamic: dynamicClient, timeout: *timeout})
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{MaxRequestsInFlight: 1}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		// Process liveness only; API collection health is reported in /metrics.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	server := &http.Server{
		Addr: *address, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      *timeout + 10*time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	log.Printf("serving /metrics on %s", *address)
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), *timeout+10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
	}
}
