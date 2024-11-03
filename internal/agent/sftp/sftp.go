//go:build windows
// +build windows

package sftp

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/agent/snapshots"
	"golang.org/x/crypto/ssh"
)

func Serve(ctx context.Context, wg *sync.WaitGroup, sftpConfig *SFTPConfig, address, port string, baseDir string) {
	defer wg.Done()
	listenAt := fmt.Sprintf("%s:%s", address, port)
	listener, err := net.Listen("tcp", listenAt)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", listenAt, err)
	}
	defer listener.Close()

	log.Printf("Listening on %v\n", listener.Addr())

	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled. Terminating SFTP listener.")
			return
		default:
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("failed to accept connection: %v", err)
				continue
			}

			go handleConnection(conn, sftpConfig, baseDir)
		}
	}
}

func handleConnection(conn net.Conn, sftpConfig *SFTPConfig, baseDir string) {
	defer conn.Close()

	server, err := url.Parse(sftpConfig.Server)
	if err != nil {
		log.Printf("failed to parse server IP: %v", err)
		return
	}

	if strings.Contains(conn.RemoteAddr().String(), server.Hostname()) {
		log.Printf("WARNING: an unregistered client has attempted to connect: %s", conn.RemoteAddr().String())
		return
	}

	sconn, chans, reqs, err := ssh.NewServerConn(conn, sftpConfig.ServerConfig)
	if err != nil {
		log.Printf("failed to perform SSH handshake: %v", err)
		return
	}

	defer sconn.Close()

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}

		go handleRequests(requests)
		go handleSFTP(channel, baseDir)
	}
}

func handlePingPong(reqs <-chan *ssh.Request) {
	for req := range reqs {
		if req.Type == "ping" {
			log.Println("Received ping request")
			err := req.Reply(true, []byte("pong"))
			if err != nil {
				log.Println("Failed to reply to ping:", err)
			}
		} else {
			log.Printf("Received unknown request type: %s", req.Type)
		}
	}
}

func handleRequests(requests <-chan *ssh.Request) {
	for req := range requests {
		if req.Type == "subsystem" && string(req.Payload[4:]) == "sftp" {
			req.Reply(true, nil)
		} else {
			req.Reply(false, nil)
		}
	}
}

func handleSFTP(channel ssh.Channel, baseDir string) {
	defer channel.Close()

	snapshot, err := snapshots.Snapshot(baseDir)
	if err != nil {
		log.Fatalf("failed to initialize snapshot: %s", err)
	}

	ctx := context.Background()
	sftpHandler, err := NewSftpHandler(ctx, baseDir, snapshot)
	if err != nil {
		_ = snapshot.Close()
		log.Fatalf("failed to initialize handler: %s", err)
	}

	snapshot.Used = true
	snapshot.LastUsedUpdate = time.Now()

	server := sftp.NewRequestServer(channel, *sftpHandler)
	if err := server.Serve(); err != nil {
		log.Printf("sftp server completed with error: %s", err)
	}

	snapshot.Used = false
	snapshot.LastUsedUpdate = time.Now()
}
