// Package deepseekfiles defines the file contract shared by route admission and upload.
package deepseekfiles

import (
	"path/filepath"
	"strings"
)

type Kind uint8

const (
	PDF Kind = iota + 1
	Document
	Spreadsheet
	Text
)

type Format struct {
	Kind    Kind
	Purpose string
}

func SupportsModel(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "deepseek-flash")
}

// Resolve only admits known document formats, never arbitrary archives or media.
// MIME is authoritative; the extension fallback supports older metadata without a detected type.
func Resolve(name, mime string) (Format, bool) {
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	var kind Kind
	switch mime {
	case "application/pdf":
		kind = PDF
	case "application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.ms-powerpoint", "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		kind = Document
	case "application/vnd.ms-excel", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		kind = Spreadsheet
	case "application/epub+zip", "application/x-mobipocket-ebook":
		return Format{Document, "user_data"}, true
	case "text/plain", "text/markdown", "text/csv", "application/json":
		kind = Text
	case "", "application/octet-stream":
		switch strings.ToLower(filepath.Ext(name)) {
		case ".pdf":
			kind = PDF
		case ".doc", ".docx", ".ppt", ".pptx":
			kind = Document
		case ".xls", ".xlsx":
			kind = Spreadsheet
		case ".epub", ".mobi":
			return Format{Document, "user_data"}, true
		case ".txt", ".md", ".csv", ".json":
			kind = Text
		}
	}
	if kind == 0 {
		return Format{}, false
	}
	purpose := "rich_layout_recognition"
	if kind == Spreadsheet || kind == Text {
		purpose = "user_data"
	}
	return Format{kind, purpose}, true
}
