//go:build linux

package proxy

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/logger"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/store"
	"github.com/sonroyaalmerol/pbs-d2d-backup/internal/utils"
)

func ExtractTokenFromRequest(r *http.Request, storeInstance *store.Store) *store.Token {
	syslogger, err := logger.InitializeSyslogger()
	if err != nil {
		log.Println(err)
	}

	if r == nil {
		return nil
	}

	token := store.Token{}

	pbsAuthCookie, err := r.Cookie("PBSAuthCookie")
	if err != nil {
		if syslogger != nil {
			syslogger.Err(fmt.Errorf("ExtractTokenFromRequest: error retrieving cookie -> %w", err).Error())
		}
		return nil
	}
	decodedAuthCookie := strings.ReplaceAll(pbsAuthCookie.Value, "%3A", ":")
	cookieSplit := strings.Split(decodedAuthCookie, ":")
	if len(cookieSplit) < 5 {
		if syslogger != nil {
			syslogger.Err(fmt.Sprintf("ExtractTokenFromRequest: error invalid cookie, less than 5 split"))
		}

		return nil
	}

	token.CSRFToken = r.Header.Get("csrfpreventiontoken")
	token.Ticket = decodedAuthCookie
	token.Username = cookieSplit[1]

	if !utils.IsValid(filepath.Join(store.DbBasePath, "pbs-d2d-token.json")) {
		apiToken, err := storeInstance.CreateAPIToken()
		if err != nil {
			errI := fmt.Errorf("ExtractTokenFromRequest: error creating API token -> %w", err)
			if syslogger != nil {
				syslogger.Err(errI.Error())
			}
			log.Println(errI)
		}

		err = apiToken.SaveToFile()
		if err != nil {
			errI := fmt.Errorf("ExtractTokenFromRequest: error saving API token to file -> %w", err)
			if syslogger != nil {
				syslogger.Err(errI.Error())
			}
			log.Println(errI)
		}
	}

	return &token
}
