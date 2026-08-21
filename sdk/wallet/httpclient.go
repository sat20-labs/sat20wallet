package wallet

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Userinfo struct {
	Username string
	Password string
}

type URL struct {
	Scheme string
	User   *Userinfo
	Host   string
	Path   string
	Query  map[string]string
}

func (p *URL) String() string {
	return p.Scheme + "://" + p.Host + "/" + p.Path
}

type HttpClient interface {
	SendGetRequest(url *URL) ([]byte, error)
	SendPostRequest(url *URL, marshalledJSON []byte) ([]byte, error)
}

// ContextHttpClient lets lifecycle-owned callers cancel in-flight requests.
// HttpClient stays unchanged so existing embedders and deterministic test
// transports remain source compatible.
type ContextHttpClient interface {
	SendGetRequestContext(ctx context.Context, url *URL) ([]byte, error)
	SendPostRequestContext(ctx context.Context, url *URL, marshalledJSON []byte) ([]byte, error)
}

type contextHTTPDeleteClient interface {
	SendDeleteRequestContext(ctx context.Context, url *URL, marshalledJSON []byte) ([]byte, error)
}

// HTTPResponseError preserves the response body for callers that use stable
// machine-readable error contracts, including DKVS error_code. Error keeps the
// historical body text so existing logs and callers remain readable.
type HTTPResponseError struct {
	StatusCode int
	Body       []byte
}

func (e *HTTPResponseError) Error() string {
	if e == nil {
		return "HTTP request failed"
	}
	if len(e.Body) != 0 {
		return string(e.Body)
	}
	return fmt.Sprintf("%d %s", e.StatusCode, http.StatusText(e.StatusCode))
}

func newHTTPResponseError(status int, body []byte) error {
	return &HTTPResponseError{StatusCode: status, Body: append([]byte(nil), body...)}
}

type NetClient struct {
	Client *http.Client
}

func (p *NetClient) SendGetRequest(u *URL) ([]byte, error) {
	return p.SendGetRequestContext(context.Background(), u)
}

func (p *NetClient) SendGetRequestContext(ctx context.Context, u *URL) ([]byte, error) {
	requestURL := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	if len(u.Query) != 0 {
		q := requestURL.Query()
		for k, v := range u.Query {
			q.Set(k, v)
		}
		requestURL.RawQuery = q.Encode()
	}
	httpRequest, err := http.NewRequestWithContext(ctx, "GET", requestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	httpRequest.Close = true
	httpRequest.Header.Set("Connection", "close")
	httpResponse, err := p.Client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	respBytes, err := io.ReadAll(httpResponse.Body)
	httpResponse.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("error reading json reply: %v", err)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return nil, newHTTPResponseError(httpResponse.StatusCode, respBytes)
	}
	if len(respBytes) == 0 {
		return nil, fmt.Errorf("server panic: %s", requestURL.String())
	}
	Log.Tracef("%v response: %s", requestURL, string(respBytes))
	return respBytes, nil
}

func (p *NetClient) SendPostRequest(u *URL, marshalledJSON []byte) ([]byte, error) {
	return p.SendPostRequestContext(context.Background(), u, marshalledJSON)
}

func (p *NetClient) SendPostRequestContext(ctx context.Context, u *URL, marshalledJSON []byte) ([]byte, error) {
	requestURL := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	bodyReader := bytes.NewReader(marshalledJSON)
	httpRequest, err := http.NewRequestWithContext(ctx, "POST", requestURL.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	httpRequest.Close = true
	httpRequest.Header.Set("Connection", "close")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := p.Client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	respBytes, err := io.ReadAll(httpResponse.Body)
	httpResponse.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("error reading json reply: %v", err)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return nil, newHTTPResponseError(httpResponse.StatusCode, respBytes)
	}
	if len(respBytes) == 0 {
		return nil, fmt.Errorf("server panic: %s", requestURL.String())
	}
	Log.Tracef("%v response: %s", requestURL, string(respBytes))
	return respBytes, nil
}

func (p *NetClient) SendDeleteRequest(u *URL, marshalledJSON []byte) ([]byte, error) {
	return p.SendDeleteRequestContext(context.Background(), u, marshalledJSON)
}

func (p *NetClient) SendDeleteRequestContext(ctx context.Context, u *URL, marshalledJSON []byte) ([]byte, error) {
	requestURL := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	if len(u.Query) != 0 {
		q := requestURL.Query()
		for k, v := range u.Query {
			q.Set(k, v)
		}
		requestURL.RawQuery = q.Encode()
	}
	httpRequest, err := http.NewRequestWithContext(ctx, "DELETE", requestURL.String(), bytes.NewBuffer(marshalledJSON))
	if err != nil {
		return nil, err
	}
	httpRequest.Close = true
	httpRequest.Header.Set("Connection", "close")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := p.Client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	respBytes, err := io.ReadAll(httpResponse.Body)
	httpResponse.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("error reading json reply: %v", err)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return nil, newHTTPResponseError(httpResponse.StatusCode, respBytes)
	}
	if len(respBytes) == 0 {
		return nil, fmt.Errorf("server panic: %s", requestURL.String())
	}
	Log.Tracef("%v response: %s", requestURL, string(respBytes))
	return respBytes, nil
}
