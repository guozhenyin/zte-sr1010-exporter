package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/example/zte-sr1010-exporter/pkg/client"
	"github.com/example/zte-sr1010-exporter/pkg/exporter"
)

type Config struct {
	URL            string `arg:"env:ZTE_URL,--url" help:"Router base URL" default:"http://192.168.10.1"`
	Username       string `arg:"env:ZTE_USERNAME,--username" help:"Login username (SR1010 接口固定 admin)" default:"admin"`
	Password       string `arg:"env:ZTE_PASSWORD,--password" help:"Login password" default:""`
	Listen         string `arg:"env:LISTEN,--listen" help:"Exporter listen address" default:":9101"`
	Timeout        int    `arg:"env:TIMEOUT,--timeout" help:"HTTP timeout seconds" default:"8"`
	Debug          bool   `arg:"env:DEBUG,--debug" help:"Enable debug logging" default:"false"`
	DisableClients bool   `arg:"env:DISABLE_CLIENTS,--disable-clients" help:"Skip the heavy LAN client table (per-device metrics)" default:"false"`
	DisableARP     bool   `arg:"env:DISABLE_ARP,--disable-arp" help:"Skip the ARP table collection" default:"false"`
}

var (
	version   = "0.1.0"
	buildTime = "unknown"
)

func main() {
	var cfg Config
	arg.MustParse(&cfg)

	if cfg.Password == "" {
		log.Fatal("ZTE_PASSWORD (or --password) is required")
	}

	log.Printf("zte-sr1010-exporter %s (built %s) starting...", version, buildTime)
	if cfg.Username == "" {
		log.Printf("target router: %s  (password-only login)", cfg.URL)
	} else {
		log.Printf("target router: %s  user: %s", cfg.URL, cfg.Username)
	}

	c := client.New(cfg.URL, cfg.Username, cfg.Password, time.Duration(cfg.Timeout)*time.Second, cfg.Debug)

	opts := exporter.Options{
		CollectClients: !cfg.DisableClients,
		CollectARP:     !cfg.DisableARP,
	}
	if cfg.DisableClients {
		log.Printf("LAN client metrics disabled (--disable-clients)")
	}
	if cfg.DisableARP {
		log.Printf("ARP table metrics disabled (--disable-arp)")
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(exporter.New(c, opts))

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("listening on %s  (/metrics)", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, nil); err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
