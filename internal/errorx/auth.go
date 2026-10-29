package errorx

import "errors"

var (
	ErrServiceBusy    = errors.New("service busy")
	ErrTooManyRequest = errors.New("too many request")

	ErrInvalidArgument       = errors.New("invalid argument")
	ErrRequestBodyTooLarge   = errors.New("request body too large")
	ErrRequestOriginFobidden = errors.New("reuqest origin forbidden")

	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserDisabled       = errors.New("user is disabled")

	ErrUsernameExists = errors.New("username already exists")
	ErrEmailExists    = errors.New("email already exists")
)
