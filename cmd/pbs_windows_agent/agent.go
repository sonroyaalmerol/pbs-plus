//go:build windows
// +build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"sync"

	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/agent/sftp"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/utils"
)

func main() {
	serverUrl := flag.String("server", "", "Server URL (e.g. https://192.168.1.1:8008)")
	flag.Parse()

	_, err := url.ParseRequestURI(*serverUrl)
	if err != nil {
		log.Println(err)
		log.Fatalf("Invalid server URL: %s", *serverUrl)
	}

	// Reserve port 33450-33476
	drives := utils.GetLocalDrives()
	ctx := context.Background()

	var wg sync.WaitGroup
	for _, driveLetter := range drives {
		rune := []rune(driveLetter)[0]

		sftpConfig, err := sftp.InitializeSFTPConfig(*serverUrl, driveLetter)
		if err != nil {
			log.Fatal(err)
		}

		port, err := utils.DriveLetterPort(rune)
		if err != nil {
			log.Fatalf("Unable to map letter to port: %v", err)
		}

		wg.Add(1)
		go sftp.Serve(ctx, &wg, sftpConfig.ServerConfig, "0.0.0.0", port, fmt.Sprintf("%s:\\", driveLetter))
	}

	wg.Wait()
}
