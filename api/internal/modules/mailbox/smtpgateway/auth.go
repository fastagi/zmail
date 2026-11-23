package smtpgateway

import "github.com/emersion/go-sasl"

// loginServer is the server side of AUTH LOGIN, which go-sasl does not provide and many mail apps
// still use: it asks for the username, then the password, each base64 encoded by go-smtp.
type loginServer struct {
	authenticate func(username, password string) error

	username string
	step     int
}

var _ sasl.Server = (*loginServer)(nil)

func (l *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch l.step {
	case 0:
		l.step = 1
		if response == nil {
			return []byte("Username:"), false, nil
		}
		// AUTH LOGIN with an initial response carries the username.
		l.username = string(response)
		l.step = 2
		return []byte("Password:"), false, nil
	case 1:
		l.username = string(response)
		l.step = 2
		return []byte("Password:"), false, nil
	case 2:
		l.step = 3
		return nil, true, l.authenticate(l.username, string(response))
	}
	return nil, true, sasl.ErrUnexpectedClientResponse
}
