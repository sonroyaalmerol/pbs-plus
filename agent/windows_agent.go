//go:build windows

package agent

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"

	"golang.org/x/crypto/ssh"
	"sgl.com/pbs-ui/agent/sftp"
	"sgl.com/pbs-ui/agent/windows"
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
	drives := windows.GetLocalDrives()
	ctx := context.Background()

	for _, driveLetter := range drives {
		rune := []rune(driveLetter)[0]

		sftpConfig := sftp.InitializeSFTPConfig(*serverUrl, driveLetter)
		if sftpConfig == nil {
			log.Fatal("SFTP config invalid")
		}

		port, err := windows.DriveLetterPort(rune)
		if err != nil {
			log.Fatalf("Unable to map letter to port: %v", err)
		}

		go sftp.Serve(ctx, &ssh.ServerConfig{}, "0.0.0.0", port, fmt.Sprintf("%s:\\", driveLetter))
	}
}
