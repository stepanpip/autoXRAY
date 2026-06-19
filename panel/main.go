package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"autoxray-panel/internal/api"
	"autoxray-panel/internal/stats"
)

//go:embed web
var webFS embed.FS

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	axDir := env("AX_DIR", "/usr/local/etc/xray")
	addr := env("PANEL_ADDR", "127.0.0.1:8088")
	xrayAPI := env("XRAY_API", "127.0.0.1:10085")
	script := env("UPDATE_SCRIPT", filepath.Join(axDir, "autoXRAY-multi", "update_clients.sh"))

	store, err := stats.Load(filepath.Join(axDir, "panel_traffic.json"))
	if err != nil {
		log.Fatalf("load store: %v", err)
	}

	go pollLoop(xrayAPI, store)

	srv := &api.Server{AxDir: axDir, XrayAPI: xrayAPI, Script: script, Store: store}

	mux := http.NewServeMux()
	mux.Handle("/api/", srv.Handler())
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("xray-panel on %s (AX_DIR=%s)", addr, axDir)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func pollLoop(xrayAPI string, store *stats.Store) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	poll := func() {
		snaps, err := stats.Query(xrayAPI)
		if err != nil {
			log.Printf("statsquery: %v", err)
			return
		}
		store.Apply(snaps)
	}
	poll()
	for range t.C {
		poll()
	}
}
