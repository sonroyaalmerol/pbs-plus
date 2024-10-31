package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/robfig/cron"
	"sgl.com/pbs-ui/controllers/jobs"
	"sgl.com/pbs-ui/controllers/targets"
	"sgl.com/pbs-ui/store"
)

//go:embed all:views
var customJsFS embed.FS

func main() {
	jobRun := flag.String("job", "", "Job ID to execute")
	flag.Parse()

	mux := http.NewServeMux()

	targetURL, err := url.Parse(store.ProxyTargetURL)
	if err != nil {
		log.Fatalf("Failed to parse target URL: %v", err)
	}

	proxy := createProxy(targetURL)

	storeInstance, err := store.Initialize()
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}

	err = storeInstance.CreateTables()
	if err != nil {
		log.Fatalf("Failed to create store tables: %v", err)
	}

	if *jobRun != "" {
		token, err := store.ReadToken()
		if err != nil {
			fmt.Println(err)
			return
		}

		if storeInstance.LastToken == nil {
			storeInstance.LastToken = token
		}

		jobTask, err := storeInstance.GetJob(*jobRun)
		if err != nil {
			fmt.Println(err)
			return
		}

		if jobTask.LastRunState == nil {
			fmt.Println("A job is still running, skipping this schedule.")
			return
		}

		_, err = jobs.RunJob(jobTask, storeInstance, nil)
		if err != nil {
			fmt.Println(err)
		}

		return
	}

	c := cron.New()
	c.AddFunc("*/5 * * * *", func() {
		store.RefreshFileToken(storeInstance)
	})
	c.Start()

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
	if err := http.ListenAndServeTLS(":8008", store.CertFile, store.KeyFile, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
