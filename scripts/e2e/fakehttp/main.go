// fakehttp stands in for an HTTP endpoint flok posts to in end-to-end tests: an ntfy topic
// (-mode ntfy, what [notify] url posts to) or the APNs provider API (-mode apns, what flok-relay
// pushes to). It listens on 127.0.0.1 on a free port, writes the port to -portfile once it
// accepts connections, and appends one line per POST to -log in a fixed shape the suites grep.
// Pure Go on purpose: Python's HTTPServer did a reverse lookup at start that stalled for half a
// minute on a CI Mac, and every push in that window was lost.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

func main() {
	mode := flag.String("mode", "ntfy", "ntfy: Title|Priority|Authorization|body; apns: path|collapse|title|body|category|badge")
	logPath := flag.String("log", "", "file to append one line per POST to")
	portFile := flag.String("portfile", "", "file to write the chosen port to once listening")
	listen := flag.String("listen", "127.0.0.1:0", "address to listen on")
	flag.Parse()
	if *logPath == "" {
		fmt.Fprintln(os.Stderr, "fakehttp: -log is needed")
		os.Exit(2)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakehttp:", err)
		os.Exit(1)
	}
	if *portFile != "" {
		port := ln.Addr().(*net.TCPAddr).Port
		tmp := *portFile + ".tmp"
		_ = os.WriteFile(tmp, []byte(fmt.Sprintf("%d\n", port)), 0o644)
		_ = os.Rename(tmp, *portFile)
	}
	var mu sync.Mutex
	dec := new(mime.WordDecoder)
	h := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var line string
		switch *mode {
		case "apns":
			var p struct {
				APS struct {
					Alert struct {
						Title string `json:"title"`
						Body  string `json:"body"`
					} `json:"alert"`
					Category string `json:"category"`
					Badge    *int   `json:"badge"`
				} `json:"aps"`
			}
			_ = json.Unmarshal(body, &p)
			badge := ""
			if p.APS.Badge != nil {
				badge = fmt.Sprint(*p.APS.Badge)
			}
			line = strings.Join([]string{r.URL.Path, r.Header.Get("apns-collapse-id"), p.APS.Alert.Title, p.APS.Alert.Body, p.APS.Category, badge}, "|")
		default:
			title := r.Header.Get("Title")
			if t, err := dec.DecodeHeader(title); err == nil { // RFC 2047, as ntfy reads it
				title = t
			}
			line = strings.Join([]string{title, r.Header.Get("Priority"), r.Header.Get("Authorization"), string(body)}, "|")
		}
		mu.Lock()
		if f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, line)
			f.Close()
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
	_ = http.Serve(ln, http.HandlerFunc(h))
}
