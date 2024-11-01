//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"

	"github.com/sonroyaalmerol/pbs-d2d-backup/agents/sftp"
	winUtils "github.com/sonroyaalmerol/pbs-d2d-backup/agents/windows/utils"
	"github.com/sonroyaalmerol/pbs-d2d-backup/utils"
	"golang.org/x/crypto/ssh"
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
	drives := winUtils.GetLocalDrives()
	ctx := context.Background()

	for _, driveLetter := range drives {
		rune := []rune(driveLetter)[0]

		sftpConfig := sftp.InitializeSFTPConfig(*serverUrl, driveLetter)
		if sftpConfig == nil {
			log.Fatal("SFTP config invalid")
		}

		port, err := utils.DriveLetterPort(rune)
		if err != nil {
			log.Fatalf("Unable to map letter to port: %v", err)
		}

		go sftp.Serve(ctx, &ssh.ServerConfig{}, "0.0.0.0", port, fmt.Sprintf("%s:\\", driveLetter))
	}
}
