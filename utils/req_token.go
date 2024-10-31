package utils

import (
	"log"
	"net/http"
	"strings"

	"github.com/sonroyaalmerol/pbs-d2d-backup/store"
)

func ExtractTokenFromRequest(r *http.Request) *store.Token {
	if r == nil {
		return nil
	}

	token := store.Token{}

	pbsAuthCookie, err := r.Cookie("PBSAuthCookie")
	if err != nil {
		log.Println(err)
		return nil
	}
	decodedAuthCookie := strings.ReplaceAll(pbsAuthCookie.Value, "%3A", ":")
	cookieSplit := strings.Split(decodedAuthCookie, ":")
	if len(cookieSplit) < 5 {
		log.Println("invalid cookie")
		log.Println(decodedAuthCookie)

		return nil
	}

	token.CSRFToken = r.Header.Get("csrfpreventiontoken")
	token.Ticket = decodedAuthCookie
	token.Username = cookieSplit[1]

	return &token
}
