package errorx

import "errors"

var (
	ErrUnauthenticated    = errors.New("authentication required")
	ErrForbidden          = errors.New("permission denied")
	ErrNotFound           = errors.New("resource not found")
	ErrConflict           = errors.New("resource conflict")
	ErrCommentsClosed     = errors.New("comments closed")
	ErrCommentUnavailable = errors.New("comment manifest unavailable")
)
