package utils

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sgl.com/pbs-ui/store"
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

func RefreshFileToken(storeInstance *store.Store) {
	if storeInstance.LastReq == nil {
		return
	}

	authCookie := ""
	for _, cookie := range storeInstance.LastReq.Cookies() {
		if cookie.Name == "PBSAuthCookie" {
			authCookie = cookie.Value
			break
		}
	}

	authCookieParts := strings.Split(authCookie, ":")
	username := authCookieParts[1]

	reqBody, err := json.Marshal(&TokenRequest{
		Username: username,
		Password: authCookie,
	})

	tokensReq, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf(
			"%s/api2/json/access/ticket",
			store.ProxyTargetURL,
		),
		bytes.NewBuffer(reqBody),
	)

	client := http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	tokensResp, err := client.Do(tokensReq)
	if err != nil {
		fmt.Println(err)
		return
	}

	tokensBody, err := io.ReadAll(tokensResp.Body)
	if err != nil {
		fmt.Println(err)
		return
	}

	var tokenStruct TokenResponse
	err = json.Unmarshal(tokensBody, &tokenStruct)
	if err != nil {
		fmt.Println(err)
		return
	}

	tokenFileContent, _ := json.Marshal(tokenStruct.Data)
	file, err := os.OpenFile(filepath.Join(store.DbBasePath, "cookies.json"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer file.Close()

	_, err = file.WriteString(string(tokenFileContent))
	if err != nil {
		fmt.Println(err)
		return
	}
}

func ReadToken() (*Token, error) {
	jsonFile, err := os.Open(filepath.Join(store.DbBasePath, "cookies.json"))
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
