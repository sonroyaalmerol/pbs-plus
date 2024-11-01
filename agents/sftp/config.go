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

	"github.com/sonroyaalmerol/pbs-d2d-backup/utils"
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

func InitializeSFTPConfig(server string, basePath string) *SFTPConfig {
	var newSftpConfig SFTPConfig

	newSftpConfig.BasePath = basePath
	newSftpConfig.Server = server

	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Println("Failed to get user config dir.")
		log.Println(err)
		return nil
	}

	configBasePath := filepath.Join(configDir, "proxmox-agent")

	err = os.MkdirAll(configBasePath, 0700)
	if err != nil {
		log.Println("Failed to create proxmox-agent dir.")
		log.Println(err)
		return nil
	}

	savedConfigPath := filepath.Join(configBasePath, fmt.Sprintf("%s-sftp.json", basePath))
	jsonFile, err := os.Open(savedConfigPath)
	if err == nil {
		byteContent, err := io.ReadAll(jsonFile)
		if err == nil {
			err = json.Unmarshal(byteContent, &newSftpConfig)
			if err == nil {
				log.Printf("Using existing config: %s\n", savedConfigPath)
			} else {
				log.Println(err)
			}
		}
	}
	jsonFile.Close()

	configSSH := &ssh.ServerConfig{
		NoClientAuth:  false,
		ServerVersion: "SSH-2.0-PBS-D2D-Agent",
		AuthLogCallback: func(conn ssh.ConnMetadata, method string, err error) {
			logAuthAttempt(conn, method, err)
		},
	}

	var privateKey, pubKey []byte
	var serverKey *string

	if len(newSftpConfig.PrivateKey) == 0 || len(newSftpConfig.PublicKey) == 0 || len(newSftpConfig.ServerKey) == 0 {
		privateKey, pubKey, err = utils.GenerateKeyPair(4096)
		if err != nil {
			log.Println("Failed to generate SSH key pair.")
			log.Println(err)
			return nil
		}

		newSftpConfig.PrivateKey = privateKey
		newSftpConfig.PublicKey = pubKey

		serverKey, err = getServerPublicKey(server, string(pubKey), basePath)
		if err != nil {
			log.Println(err)
			return nil
		}

		newSftpConfig.ServerKey = []byte(*serverKey)

		knownHosts, err := os.OpenFile(filepath.Join(configBasePath, fmt.Sprintf("%s-sftp.json", basePath)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			log.Println(err)
		}
		defer knownHosts.Close()

		jsonContent, err := json.Marshal(newSftpConfig)
		if err == nil {
			if _, err = knownHosts.Write(jsonContent); err != nil {
				log.Println(err)
			}
		}
	} else {
		privateKey = newSftpConfig.PrivateKey
		pubKey = newSftpConfig.PublicKey
		serverKeyStr := string(newSftpConfig.ServerKey)

		serverKey = &serverKeyStr
	}

	parsedSigner, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil
	}

	configSSH.AddHostKey(parsedSigner)

	parsedServerKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(*serverKey))
	if err != nil {
		log.Println(*serverKey)
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

	newSftpConfig.ServerConfig = configSSH

	return &newSftpConfig
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
	hostname, _ := os.Hostname()
	reqBody, err := json.Marshal(map[string]string{
		"public_key": publicKey,
		"base_path":  basePath,
		"hostname":   hostname,
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
		fmt.Println(string(newAgentBody))
		return nil, err
	}

	return &newAgentStruct.PublicKey, nil
}

func comparePublicKeys(key1, key2 ssh.PublicKey) bool {
	return string(key1.Marshal()) == string(key2.Marshal())
}
