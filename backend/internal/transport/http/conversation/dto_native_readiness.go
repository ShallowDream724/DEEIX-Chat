package conversation

import appconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/conversation"

type NativeReadinessRequest struct {
	Model                 string   `json:"model,omitempty" binding:"max=128"`
	FileIDs               []string `json:"fileIDs,omitempty" binding:"omitempty,dive,min=1,max=128"`
	ConversationID        string   `json:"conversationID,omitempty" binding:"omitempty,max=32"`
	ParentMessagePublicID string   `json:"parentMessagePublicID,omitempty" binding:"omitempty,max=32"`
	SelectedToolIDs       []uint   `json:"selectedToolIDs,omitempty" binding:"max=128"`
}

type NativeFileReadinessResponse struct {
	FileID   string `json:"fileID"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty" enums:"unsupported,budget_exceeded,unavailable"`
}

type NativeReadinessResponse struct {
	Files            []NativeFileReadinessResponse      `json:"files"`
	MessageFileCount int                                `json:"messageFileCount"`
	Limits           AttachmentReadinessLimitsResponse  `json:"limits"`
	Issues           []AttachmentReadinessIssueResponse `json:"issues"`
}

type AttachmentReadinessLimitsResponse struct {
	RequestBytes     int64 `json:"requestBytes"`
	ImageCount       int   `json:"imageCount"`
	ImageBytes       int64 `json:"imageBytes"`
	ImageSingleBytes int64 `json:"imageSingleBytes"`
	NativeCount      int   `json:"nativeCount"`
	NativeBytes      int64 `json:"nativeBytes"`
	PDFSingleBytes   int64 `json:"pdfSingleBytes"`
	PDFBytes         int64 `json:"pdfBytes"`
	PDFPages         int   `json:"pdfPages"`
	NonPDFBytes      int64 `json:"nonPDFBytes"`
}

type AttachmentReadinessIssueResponse struct {
	Dimension    string `json:"dimension"`
	Actual       int64  `json:"actual"`
	Limit        int64  `json:"limit"`
	FileName     string `json:"fileName,omitempty"`
	FileCategory string `json:"fileCategory,omitempty" enums:"image,pdf,document,audio,video,other"`
	Scope        string `json:"scope,omitempty" enums:"current,history,context"`
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
	issues := make([]AttachmentReadinessIssueResponse, len(result.Issues))
	for i, issue := range result.Issues {
		issues[i] = AttachmentReadinessIssueResponse{
			Dimension: issue.Dimension, Actual: issue.Actual, Limit: issue.Limit,
			FileName: issue.FileName, FileCategory: issue.FileCategory, Scope: issue.Scope,
		}
	}
	limits := result.Limits
	return NativeReadinessResponse{
		Files: files, MessageFileCount: result.MessageFileCount, Issues: issues,
		Limits: AttachmentReadinessLimitsResponse{
			RequestBytes: limits.RequestBytes, ImageCount: limits.ImageCount, ImageBytes: limits.ImageBytes,
			ImageSingleBytes: limits.ImageSingleBytes, NativeCount: limits.NativeCount, NativeBytes: limits.NativeBytes,
			PDFSingleBytes: limits.PDFSingleBytes, PDFBytes: limits.PDFBytes, PDFPages: limits.PDFPages, NonPDFBytes: limits.NonPDFBytes,
		},
	}
}
