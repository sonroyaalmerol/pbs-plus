package proxy

import (
	"log"
	"net/http"
	"strings"

	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/store"
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

	tokenForBackground := store.Token{
		CSRFToken: token.CSRFToken,
		Ticket:    token.Ticket,
		Username:  token.Username,
	}

	tokenForBackground.Refresh()
	err = tokenForBackground.SaveToFile()
	if err != nil {
		log.Println(err)
	}

	return &token
}
