//go:build windows

package sftp

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/pkg/sftp"
	"github.com/sonroyaalmerol/pbs-d2d-backup/agents/windows/utils"
	"golang.org/x/crypto/ssh"
)

func Serve(ctx context.Context, wg *sync.WaitGroup, sshConfig *ssh.ServerConfig, address, port string, baseDir string) {
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

			go handleConnection(conn, sshConfig, baseDir)
		}
	}
}

func handleConnection(conn net.Conn, sshConfig *ssh.ServerConfig, baseDir string) {
	defer conn.Close()

	sconn, chans, reqs, err := ssh.NewServerConn(conn, sshConfig)
	if err != nil {
		log.Printf("failed to perform SSH handshake: %v", err)
		log.Println(sshConfig)
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

	snapshot, err := utils.Snapshot(fmt.Sprintf("%s:\\", baseDir))
	if err != nil {
		log.Fatalf("failed to initialize snapshot: %s", err)
	}

	ctx := context.Background()
	sftpHandler, err := NewSftpHandler(ctx, baseDir, snapshot)
	if err != nil {
		log.Fatalf("failed to initialize handler: %s", err)
	}

	server := sftp.NewRequestServer(channel, *sftpHandler)
	if err := server.Serve(); err != nil {
		log.Printf("sftp server completed with error: %s", err)
	}
}
