package subtitles

import (
	"bigflix/internal/catalog"
	"net/http"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// Adapt the fingerprinted client to net/http so provider limits, redirect
// validation and fixture transports remain shared with the normal request path.
type browserTransport struct {
	client tlsclient.HttpClient
	err    error
}

func newBrowserClient() *http.Client {
	public := catalog.PublicClient().Transport.(*http.Transport)
	c, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(),
		tlsclient.WithClientProfile(profiles.Chrome_150),
		tlsclient.WithTimeoutSeconds(20),
		tlsclient.WithCookieJar(tlsclient.NewCookieJar()),
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithDisableHttp3(),
		tlsclient.WithDialContext(public.DialContext),
	)
	return &http.Client{Transport: &browserTransport{client: c, err: err}, Timeout: 20 * time.Second}
}

func (t *browserTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.err != nil {
		return nil, t.err
	}
	r, err := fhttp.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), req.Body)
	if err != nil {
		return nil, err
	}
	r.Header = fhttp.Header{}
	for key, values := range req.Header {
		r.Header[key] = append([]string(nil), values...)
	}
	r.Header[fhttp.HeaderOrderKey] = []string{"user-agent", "accept", "accept-language", "referer", "cookie"}
	resp, err := t.client.Do(r)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		Status: resp.Status, StatusCode: resp.StatusCode,
		Proto: resp.Proto, ProtoMajor: resp.ProtoMajor, ProtoMinor: resp.ProtoMinor,
		Header: http.Header(resp.Header), Body: resp.Body,
		ContentLength: resp.ContentLength, Request: req,
	}, nil
}

func (t *browserTransport) CloseIdleConnections() {
	if t.client != nil {
		t.client.CloseIdleConnections()
	}
}
