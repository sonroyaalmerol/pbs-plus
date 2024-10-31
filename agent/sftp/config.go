package sftp

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type SFTPConfig struct {
	SSHKey       string
	ServerConfig *ssh.ServerConfig
}

func InitializeSFTPConfig(server string, basePath string) *SFTPConfig {
	privateKey, pubKey, err := GenerateKeyPair(4096)
	if err != nil {
		log.Println("Failed to generate SSH key pair.")
		log.Println(err)
		return nil
	}

	configSSH := &ssh.ServerConfig{
		NoClientAuth:  false,
		ServerVersion: "PBS-D2D-Agent",
		AuthLogCallback: func(conn ssh.ConnMetadata, method string, err error) {
			logAuthAttempt(conn, method, err)
		},
	}
	configSSH.AddHostKey(*privateKey)

	serverKey, err := getServerPublicKey(server, string(pubKey), basePath)
	if err != nil {
		log.Println(err)
		return nil
	}

	parsedServerKey, err := ssh.ParsePublicKey([]byte(*serverKey))
	if err != nil {
		log.Println(err)
		return nil
	}

	configSSH.PublicKeyCallback = func(conn ssh.ConnMetadata, auth ssh.PublicKey) (*ssh.Permissions, error) {
		if comparePublicKeys(parsedServerKey, auth) {
			return &ssh.Permissions{
				Extensions: map[string]string{
					"pubkey-fp": ssh.FingerprintSHA256(auth),
				},
			}, nil
		}
		return nil, fmt.Errorf("unknown public key for %q", conn.User())
	}

	return &SFTPConfig{
		ServerConfig: configSSH,
	}
}

func logAuthAttempt(conn ssh.ConnMetadata, _ string, err error) {
	if err != nil {
		log.Printf("User: %s Authentication Attempt from %s", conn.User(), conn.RemoteAddr())
	} else {
		log.Printf("User: %s Authentication Accepted from %s", conn.User(), conn.RemoteAddr())
	}
}

type NewAgentResp struct {
	PublicKey string `json:"public_key"`
}

func getServerPublicKey(server string, publicKey string, basePath string) (*string, error) {
	reqBody, err := json.Marshal(map[string]string{
		"public_key": publicKey,
		"base_path":  basePath,
	})
	if err != nil {
		return nil, err
	}

	newAgentReq, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf(
			"%s/api2/json/d2d/target",
			strings.TrimSuffix(server, "/"),
		),
		bytes.NewBuffer(reqBody),
	)

	client := http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	newAgentResp, err := client.Do(newAgentReq)
	if err != nil {
		return nil, err
	}

	newAgentBody, err := io.ReadAll(newAgentResp.Body)
	if err != nil {
		return nil, err
	}

	var newAgentStruct NewAgentResp
	err = json.Unmarshal(newAgentBody, &newAgentStruct)
	if err != nil {
		fmt.Println(newAgentBody)
		return nil, err
	}

	return &newAgentStruct.PublicKey, nil
}

func comparePublicKeys(key1, key2 ssh.PublicKey) bool {
	return string(key1.Marshal()) == string(key2.Marshal())
}
