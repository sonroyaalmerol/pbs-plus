//go:build windows
// +build windows

package sftp

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/utils"
	"golang.org/x/crypto/ssh"
)

type SFTPConfig struct {
	ServerConfig *ssh.ServerConfig
	PrivateKey   []byte `json:"private_key"`
	PublicKey    []byte `json:"public_key"`
	ServerKey    []byte `json:"server_key"`
	Server       string `json:"server"`
	BasePath     string `json:"base_path"`
}

func InitializeSFTPConfig(serverUrl string, driveLetter string) (*SFTPConfig, error) {
	newSftpConfig := SFTPConfig{
		BasePath: driveLetter,
		Server:   serverUrl,
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return &newSftpConfig, fmt.Errorf("ReadFileConfig: failed to get user config directory -> %w", err)
	}

	configBasePath := filepath.Join(configDir, "proxmox-agent")

	err = os.MkdirAll(configBasePath, 0700)
	if err != nil {
		return &newSftpConfig, fmt.Errorf("ReadFileConfig: failed to create proxmox-agent directory -> %w", err)
	}

	savedConfigPath := filepath.Join(configBasePath, fmt.Sprintf("%s-sftp.json", driveLetter))
	jsonFile, err := os.Open(savedConfigPath)
	if err != nil {
		return &newSftpConfig, fmt.Errorf("ReadFileConfig: failed to open json file -> %w", err)
	}
	defer jsonFile.Close()

	byteContent, err := io.ReadAll(jsonFile)
	if err != nil {
		return &newSftpConfig, fmt.Errorf("ReadFileConfig: failed to read json file content -> %w", err)
	}

	var existingConfig SFTPConfig
	err = json.Unmarshal(byteContent, &existingConfig)
	if err != nil {
		return &newSftpConfig, fmt.Errorf("ReadFileConfig: invalid json file (%s) -> %w", string(byteContent), err)
	}

	return &existingConfig, nil
}

func (config *SFTPConfig) PopulateKeys() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("ReadFileConfig: failed to get user config directory -> %w", err)
	}

	configBasePath := filepath.Join(configDir, "proxmox-agent")

	configSSH := &ssh.ServerConfig{
		NoClientAuth:  false,
		ServerVersion: "SSH-2.0-PBS-D2D-Agent",
		AuthLogCallback: func(conn ssh.ConnMetadata, method string, err error) {
			logAuthAttempt(conn, method, err)
		},
	}

	if len(config.PrivateKey) == 0 || len(config.PublicKey) == 0 || len(config.ServerKey) == 0 {
		privateKey, pubKey, err := utils.GenerateKeyPair(4096)
		if err != nil {
			return fmt.Errorf("InitializeSFTPConfig: failed to generate SSH key pair -> %w", err)
		}

		config.PrivateKey = privateKey
		config.PublicKey = pubKey

		serverKey, err := getServerPublicKey(config.Server, string(pubKey), config.BasePath)
		if err != nil {
			return fmt.Errorf("InitializeSFTPConfig: failed to get server public ssh key -> %w", err)
		}

		config.ServerKey = []byte(*serverKey)

		knownHosts, err := os.OpenFile(filepath.Join(configBasePath, fmt.Sprintf("%s-sftp.json", config.BasePath)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			log.Println(err)
		}
		defer knownHosts.Close()

		jsonContent, err := json.Marshal(config)
		if err == nil {
			if _, err = knownHosts.Write(jsonContent); err != nil {
				log.Println(err)
			}
		}
	}

	parsedSigner, err := ssh.ParsePrivateKey(config.PrivateKey)
	if err != nil {
		return fmt.Errorf("InitializeSFTPConfig: failed to parse private key to signer -> %w", err)
	}

	configSSH.AddHostKey(parsedSigner)

	parsedServerKey, _, _, _, err := ssh.ParseAuthorizedKey(config.ServerKey)
	if err != nil {
		return fmt.Errorf("InitializeSFTPConfig: failed to parse server key -> %w", err)
	}

	configSSH.PublicKeyCallback = func(conn ssh.ConnMetadata, auth ssh.PublicKey) (*ssh.Permissions, error) {
		if comparePublicKeys(parsedServerKey, auth) {
			return &ssh.Permissions{
				Extensions: map[string]string{
					"pubkey-fp": ssh.FingerprintSHA256(auth),
				},
			}, nil
		}
		return nil, fmt.Errorf("InitializeSFTPConfig: unknown public key for %s -> %w", conn.RemoteAddr().String(), err)
	}

	config.ServerConfig = configSSH

	return nil
}

func logAuthAttempt(conn ssh.ConnMetadata, _ string, err error) {
	if err != nil {
		log.Printf("Agent: %s Authentication Attempt from %s", conn.User(), conn.RemoteAddr())
	} else {
		log.Printf("Agent: %s Authentication Accepted from %s", conn.User(), conn.RemoteAddr())
	}
}

type NewAgentResp struct {
	PublicKey string `json:"public_key"`
}

func getServerPublicKey(server string, publicKey string, basePath string) (*string, error) {
	hostname, _ := os.Hostname()
	reqBody, err := json.Marshal(map[string]string{
		"public_key": publicKey,
		"base_path":  basePath,
		"hostname":   hostname,
	})
	if err != nil {
		return nil, fmt.Errorf("getServerPublicKey: error json marshal for body request -> %w", err)
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
		return nil, fmt.Errorf("getServerPublicKey: error executing http request -> %w", err)
	}

	defer func() {
		_, _ = io.Copy(io.Discard, newAgentResp.Body)
		newAgentResp.Body.Close()
	}()

	newAgentBody, err := io.ReadAll(newAgentResp.Body)
	if err != nil {
		return nil, fmt.Errorf("getServerPublicKey: error getting body from response -> %w", err)
	}

	var newAgentStruct NewAgentResp
	err = json.Unmarshal(newAgentBody, &newAgentStruct)
	if err != nil {
		return nil, fmt.Errorf("getServerPublicKey: error unmarshal json from body -> %w", err)
	}

	return &newAgentStruct.PublicKey, nil
}

func comparePublicKeys(key1, key2 ssh.PublicKey) bool {
	return string(key1.Marshal()) == string(key2.Marshal())
}
