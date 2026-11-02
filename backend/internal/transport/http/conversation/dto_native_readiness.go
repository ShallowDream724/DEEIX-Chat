package conversation

import appconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/conversation"

type NativeReadinessRequest struct {
	Model          string   `json:"model" binding:"max=128"`
	FileIDs        []string `json:"fileIDs" binding:"required,min=1,max=100,dive,required,max=128"`
	ConversationID string   `json:"conversationID,omitempty" binding:"omitempty,max=32"`
}

type NativeFileReadinessResponse struct {
	FileID   string `json:"fileID"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty" enums:"unsupported,budget_exceeded,unavailable"`
}

type NativeReadinessResponse struct {
	Files []NativeFileReadinessResponse `json:"files"`
}

type NativeReadinessResponseDoc struct {
	ErrorMsg string                  `json:"errorMsg"`
	Data     NativeReadinessResponse `json:"data"`
}

func toNativeReadinessResponse(result *appconversation.NativeReadinessResult) NativeReadinessResponse {
	files := make([]NativeFileReadinessResponse, len(result.Files))
	for i, file := range result.Files {
		files[i] = NativeFileReadinessResponse{FileID: file.FileID, Eligible: file.Eligible, Reason: file.Reason}
	}
	return NativeReadinessResponse{Files: files}
}
