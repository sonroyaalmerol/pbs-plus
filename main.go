package main

import (
	"embed"
	"log"
	"net/http"
	"net/url"

	"sgl.com/pbs-ui/controllers"
	"sgl.com/pbs-ui/controllers/jobs"
	"sgl.com/pbs-ui/controllers/targets"
	"sgl.com/pbs-ui/store"
)

//go:embed all:views/js
var customJsFS embed.FS

func main() {
	mux := http.NewServeMux()

	targetURL, err := url.Parse(store.ProxyTargetURL)
	if err != nil {
		log.Fatalf("Failed to parse target URL: %v", err)
	}

	proxy := controllers.CreateProxy(targetURL, &customJsFS)

	storeInstance, err := store.Initialize()
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}

	err = storeInstance.CreateTables()
	if err != nil {
		log.Fatalf("Failed to create store tables: %v", err)
	}

	mux.HandleFunc("/api2/json/d2d/backup", jobs.D2DJobHandler(storeInstance))
	mux.HandleFunc("/api2/json/d2d/target", targets.D2DTargetHandler(storeInstance))

	mux.HandleFunc("/api2/extjs/d2d/backup/{job}", jobs.ExtJsJobRunHandler(storeInstance))

	mux.HandleFunc("/api2/extjs/config/d2d-target", targets.ExtJsTargetHandler(storeInstance))
	mux.HandleFunc("/api2/extjs/config/d2d-target/{target}", targets.ExtJsTargetSingleHandler(storeInstance))

	mux.HandleFunc("/api2/extjs/config/disk-backup-job", jobs.ExtJsJobHandler(storeInstance))
	mux.HandleFunc("/api2/extjs/config/disk-backup-job/{job}", jobs.ExtJsJobSingleHandler(storeInstance))

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	}))

	log.Println("Starting proxy server on :8008")
	if err := http.ListenAndServeTLS(":8008", "/etc/proxmox-backup/proxy.pem", "/etc/proxmox-backup/proxy.key", mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
