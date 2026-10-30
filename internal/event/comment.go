package event

const CommentSubmitted = "comment.submitted"

type CommentSubmittedPayload struct {
	CommentID int64 `json:"comment_id,string"`
}
