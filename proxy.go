package main

import (
	"bytes"
	"compress/flate"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"sgl.com/pbs-ui/store"
)

func createProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director

	proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	proxy.Director = func(req *http.Request) {
		originalDirector(req)
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		if strings.HasSuffix(resp.Request.URL.Path, store.ModifiedFilePath) {
			var body []byte
			var err error

			body, err = io.ReadAll(flate.NewReader(resp.Body))
			if err != nil {
				return err
			}
			resp.Body.Close()

			modifiedContent := append(body, []byte("\n// Modified by proxy\n")...)
			modifiedContent = append(modifiedContent, compileCustomJS()...)

			resp.Body = io.NopCloser(bytes.NewReader(modifiedContent))
			resp.Header.Del("Content-Length")   // Remove Content-Length to enable chunked encoding
			resp.Header.Del("Content-Encoding") // Clear Content-Encoding to send plain text
		}

		return nil
	}

	return proxy
}
