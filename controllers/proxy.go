package controllers

import (
	"bytes"
	"compress/flate"
	"crypto/tls"
	"embed"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"sgl.com/pbs-ui/store"
	"sgl.com/pbs-ui/views"
)

// createProxy creates a reverse proxy with a transport that allows response modification.
func CreateProxy(target *url.URL, jsFs *embed.FS) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director

	// Allow insecure HTTPS connections to the target server
	proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	// Modify the director to handle request passing to the target server
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
	}

	// Modify the response function for intercepting and changing content
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Check if the requested file is the specific JS file to modify
		if strings.HasSuffix(resp.Request.URL.Path, store.ModifiedFilePath) {
			// Modify the JS file content before sending it back to the client
			var body []byte
			var err error

			body, err = io.ReadAll(flate.NewReader(resp.Body))
			if err != nil {
				return err
			}
			resp.Body.Close()

			modifiedContent := append(body, []byte("\n// Modified by proxy\n")...)
			modifiedContent = append(modifiedContent, views.CompileCustomJS(jsFs, "")...)

			// Update response body without setting Content-Length
			resp.Body = io.NopCloser(bytes.NewReader(modifiedContent))
			resp.Header.Del("Content-Length")   // Remove Content-Length to enable chunked encoding
			resp.Header.Del("Content-Encoding") // Clear Content-Encoding to send plain text
		}

		return nil
	}

	return proxy
}
