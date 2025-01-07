package common

type Userinfo struct {
	Username    string
	Password    string
}

type URL struct {
	Scheme      string
	User        *Userinfo // username and password information
	Host        string    // host or host:port (see Hostname and Port methods)
	Path        string    // path (relative paths may omit leading slash)
}

type HttpClient interface {
	SendGetRequest(url *URL) ([]byte, error)
	SendPostRequest(url *URL, marshalledJSON []byte) ([]byte, error)
}

