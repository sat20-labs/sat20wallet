package wallet

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
)


type Userinfo struct {
	Username    string
	Password    string
}

type URL struct {
	Scheme      string
	User        *Userinfo // username and password information
	Host        string    // host or host:port (see Hostname and Port methods)
	Path        string    // path (relative paths may omit leading slash)
	Query     	map[string]string
}

func (p *URL) String() string {
	return p.Scheme + "://" + p.Host + "/" + p.Path
}

type HttpClient interface {
	SendGetRequest(url *URL) ([]byte, error)
	SendPostRequest(url *URL, marshalledJSON []byte) ([]byte, error)
}



type NetClient struct {
	Client *http.Client
}

func (p *NetClient) SendGetRequest(u *URL) ([]byte, error) {

	url := url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   u.Path,
	}

	if len(u.Query) != 0 {
		q := url.Query()
		for k, v := range u.Query {
			q.Set(k, v)
		}
		url.RawQuery = q.Encode()
	}

	httpResponse, err := p.Client.Get(url.String())
	if err != nil {
		return nil, err
	}

	// Read the raw bytes and close the response.
	respBytes, err := io.ReadAll(httpResponse.Body)
	httpResponse.Body.Close()
	if err != nil {
		err = fmt.Errorf("error reading json reply: %v", err)
		return nil, err
	}

	// Handle unsuccessful HTTP responses
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		// Generate a standard error to return if the server body is
		// empty.  This should not happen very often, but it's better
		// than showing nothing in case the target server has a poor
		// implementation.
		if len(respBytes) == 0 {
			return nil, fmt.Errorf("%d %s", httpResponse.StatusCode,
				http.StatusText(httpResponse.StatusCode))
		}
		return nil, fmt.Errorf("%s", respBytes)
	}

	if len(respBytes) == 0 {
		return nil, fmt.Errorf("server panic: %s", url.String())
	}

	// Unmarshal the response.
	// var resp btcjson.Response
	// if err := json.Unmarshal(respBytes, &resp); err != nil {
	// 	return nil, err
	// }

	// if resp.Error != nil {
	// 	return nil, resp.Error
	// }
	// return resp.Result, nil
	Log.Tracef("%v response: %s", url, string(respBytes))
	return respBytes, nil
}

// sendPostRequest sends the marshalled JSON command using HTTP-POST mode
// to the server described in the passed config struct.  It also attempts to
// unmarshal the response as a JSON response and returns either the result
// field or the error field depending on whether or not there is an error.
func (p *NetClient) SendPostRequest(u *URL, marshalledJSON []byte) ([]byte, error) {
	url := url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   u.Path,
	}

	bodyReader := bytes.NewReader(marshalledJSON)
	httpRequest, err := http.NewRequest("POST", url.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	httpRequest.Close = true
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := p.Client.Do(httpRequest)
	if err != nil {
		return nil, err
	}

	// Read the raw bytes and close the response.
	respBytes, err := io.ReadAll(httpResponse.Body)
	httpResponse.Body.Close()
	if err != nil {
		err = fmt.Errorf("error reading json reply: %v", err)
		return nil, err
	}

	// Handle unsuccessful HTTP responses
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		// Generate a standard error to return if the server body is
		// empty.  This should not happen very often, but it's better
		// than showing nothing in case the target server has a poor
		// implementation.
		if len(respBytes) == 0 {
			return nil, fmt.Errorf("%d %s", httpResponse.StatusCode,
				http.StatusText(httpResponse.StatusCode))
		}
		return nil, fmt.Errorf("%s", respBytes)
	}

	if len(respBytes) == 0 {
		return nil, fmt.Errorf("server panic: %s", url.String())
	}

	// Unmarshal the response.
	// var resp btcjson.Response
	// if err := json.Unmarshal(respBytes, &resp); err != nil {
	// 	return nil, err
	// }

	// if resp.Error != nil {
	// 	return nil, resp.Error
	// }
	// return resp.Result, nil
	Log.Tracef("%v response: %s", url, string(respBytes))
	return respBytes, nil
}
