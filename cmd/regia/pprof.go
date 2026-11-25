package main

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"time"
)

// startPprof espone i profili di Go (heap, CPU, goroutine) su un indirizzo locale, solo se
// REGIA_PPROF è impostata (es. 127.0.0.1:8189): serve a misurare la memoria sotto carico
// (docs/benchmark.md). Rifiuta indirizzi che non siano di loopback.
func startPprof(log *slog.Logger) {
	addr := os.Getenv("REGIA_PPROF")
	if addr == "" {
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		log.Warn("REGIA_PPROF ignorata: serve un indirizzo di loopback (127.0.0.1:porta)", "valore", addr)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	go func() {
		srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		log.Info("profili di Go attivi", "indirizzo", addr)
		if err := srv.ListenAndServe(); err != nil {
			log.Warn("profili di Go", "errore", err)
		}
	}()
}
