package wallet

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sat20-labs/sat20wallet/common"
)

type NetClient struct {
	Client *http.Client
}

func (p *NetClient) SendGetRequest(u *common.URL) ([]byte, error) {

	url := url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   u.Path,
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

	// Unmarshal the response.
	// var resp btcjson.Response
	// if err := json.Unmarshal(respBytes, &resp); err != nil {
	// 	return nil, err
	// }

	// if resp.Error != nil {
	// 	return nil, resp.Error
	// }
	// return resp.Result, nil
	return respBytes, nil
}

// sendPostRequest sends the marshalled JSON command using HTTP-POST mode
// to the server described in the passed config struct.  It also attempts to
// unmarshal the response as a JSON response and returns either the result
// field or the error field depending on whether or not there is an error.
func (p *NetClient) SendPostRequest(u *common.URL, marshalledJSON []byte) ([]byte, error) {
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

	// Unmarshal the response.
	// var resp btcjson.Response
	// if err := json.Unmarshal(respBytes, &resp); err != nil {
	// 	return nil, err
	// }

	// if resp.Error != nil {
	// 	return nil, resp.Error
	// }
	// return resp.Result, nil
	return respBytes, nil
}
