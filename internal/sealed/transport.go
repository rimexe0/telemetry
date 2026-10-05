package sealed

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Transport sends a device's requests to Server sealed through Relay, so
// the device does not depend on the private network the server's direct
// address needs. Requests to anything else, and requests the relay does not
// pass on, go out directly. When the relay cannot be reached or refuses the
// request, it is sent to the direct address instead.
type Transport struct {
	// Server is the server's direct address; only requests to it are sealed.
	Server *url.URL
	// Relay is the relay's address, such as https://relay.example.com.
	Relay  string
	Sealer *Sealer
	// Direct carries everything else, and the relay's own traffic.
	Direct http.RoundTripper
}

func (t *Transport) direct() http.RoundTripper {
	if t.Direct != nil {
		return t.Direct
	}
	return http.DefaultTransport
}

func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != t.Server.Scheme || request.URL.Host != t.Server.Host || !reachable(request.URL.Path) {
		return t.direct().RoundTrip(request)
	}
	var body []byte
	if request.Body != nil {
		read, err := io.ReadAll(request.Body)
		request.Body.Close()
		if err != nil {
			return nil, err
		}
		body = read
	}
	if response, err := t.sealed(request, body); err == nil {
		return response, nil
	}
	direct := request.Clone(request.Context())
	direct.Body = io.NopCloser(bytes.NewReader(body))
	direct.ContentLength = int64(len(body))
	return t.direct().RoundTrip(direct)
}

func (t *Transport) sealed(request *http.Request, body []byte) (*http.Response, error) {
	head := Head{Method: request.Method, Path: request.URL.RequestURI(), Headers: map[string]string{}, At: time.Now().UnixMilli(), ID: randomID()}
	for _, name := range passed {
		if value := request.Header.Get(name); value != "" {
			head.Headers[name] = value
		}
	}
	message, open, err := t.Sealer.Seal(head, body)
	if err != nil {
		return nil, err
	}
	relay, err := http.NewRequestWithContext(request.Context(), http.MethodPost, strings.TrimRight(t.Relay, "/")+"/", bytes.NewReader(message))
	if err != nil {
		return nil, err
	}
	relay.Header.Set("Content-Type", "application/octet-stream")
	response, err := t.direct().RoundTrip(relay)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay answered %s", response.Status)
	}
	sealed, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	answer, inner, err := open(sealed)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	if answer.ContentType != "" {
		header.Set("Content-Type", answer.ContentType)
	}
	header.Set("Content-Length", strconv.Itoa(len(inner)))
	return &http.Response{
		Status: fmt.Sprintf("%d %s", answer.Status, http.StatusText(answer.Status)), StatusCode: answer.Status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: header,
		Body: io.NopCloser(bytes.NewReader(inner)), ContentLength: int64(len(inner)), Request: request,
	}, nil
}
