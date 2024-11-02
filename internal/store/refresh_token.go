package store

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
)

type Token struct {
	CSRFToken string `json:"CSRFPreventionToken"`
	Ticket    string `json:"ticket"`
	Username  string `json:"username"`
}

type TokenResponse struct {
	Data Token `json:"data"`
}

type TokenRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (token *Token) Refresh() {
	authCookie := token.Ticket
	decodedAuthCookie := strings.ReplaceAll(authCookie, "%3A", ":")

	authCookieParts := strings.Split(decodedAuthCookie, ":")
	if len(authCookieParts) < 5 {
		log.Printf("Invalid cookie: %s\n", authCookie)
		return
	}

	username := authCookieParts[1]

	reqBody, err := json.Marshal(&TokenRequest{
		Username: username,
		Password: decodedAuthCookie,
	})

	tokensReq, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf(
			"%s/api2/json/access/ticket",
			ProxyTargetURL,
		),
		bytes.NewBuffer(reqBody),
	)

	tokensReq.Header.Add("Content-Type", "application/json")

	client := http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	tokensResp, err := client.Do(tokensReq)
	if err != nil {
		log.Println(err)
		return
	}

	tokensBody, err := io.ReadAll(tokensResp.Body)
	if err != nil {
		log.Println(err)
		return
	}

	var tokenStruct TokenResponse
	err = json.Unmarshal(tokensBody, &tokenStruct)
	if err != nil {
		log.Println(err)
		return
	}

	token.CSRFToken = tokenStruct.Data.CSRFToken
	token.Ticket = tokenStruct.Data.Ticket
	token.Username = tokenStruct.Data.Username
}

func (token *Token) SaveToFile() error {
	tokenFileContent, _ := json.Marshal(token)
	file, err := os.OpenFile(filepath.Join(DbBasePath, "cookies.json"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(string(tokenFileContent))
	if err != nil {
		return err
	}

	return nil
}

func GetTokenFromFile() (*Token, error) {
	jsonFile, err := os.Open(filepath.Join(DbBasePath, "cookies.json"))
	if err != nil {
		return nil, err
	}
	defer jsonFile.Close()

	byteValue, err := io.ReadAll(jsonFile)
	if err != nil {
		return nil, err
	}

	var result Token
	err = json.Unmarshal([]byte(byteValue), &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}
