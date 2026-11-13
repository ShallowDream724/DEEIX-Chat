package conversation

import "time"

type SaveMessageRevisionRequest struct {
	Content           string    `json:"content"`
	FileIDs           []string  `json:"fileIDs" binding:"required,max=100"`
	ExpectedUpdatedAt time.Time `json:"expectedUpdatedAt" binding:"required"`
	RevisionID        string    `json:"revisionID" binding:"required,uuid"`
}

type MessageRevisionResponse struct {
	Message                     MessageResponse `json:"message"`
	SelectedLeafMessagePublicID string          `json:"selectedLeafMessagePublicID"`
}

type MessageRevisionResponseDoc struct {
	Code    int                     `json:"code"`
	Message string                  `json:"message"`
	Data    MessageRevisionResponse `json:"data"`
}
