package deepseekfiles

import "testing"

func TestFormatContract(t *testing.T) {
	for _, tc := range []struct {
		name, mime, purpose string
		kind                Kind
	}{
		{"a.pdf", "application/pdf", "rich_layout_recognition", PDF},
		{"a.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "rich_layout_recognition", Document},
		{"a.ppt", "application/vnd.ms-powerpoint", "rich_layout_recognition", Document},
		{"a.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "user_data", Spreadsheet},
		{"a.epub", "application/epub+zip", "user_data", Document},
		{"a.mobi", "application/x-mobipocket-ebook", "user_data", Document},
		{"a.txt", "text/plain; charset=utf-8", "user_data", Text},
		{"legacy.docx", "", "rich_layout_recognition", Document},
	} {
		got, ok := Resolve(tc.name, tc.mime)
		if !ok || got.Kind != tc.kind || got.Purpose != tc.purpose {
			t.Errorf("%s: %+v %v", tc.name, got, ok)
		}
	}
	for _, tc := range [][2]string{{"a.zip", "application/zip"}, {"renamed.docx", "application/zip"}, {"a.wav", "audio/wav"}, {"a.mp4", "video/mp4"}, {"a.exe", "application/x-executable"}, {"a.azw3", "application/octet-stream"}} {
		if _, ok := Resolve(tc[0], tc[1]); ok {
			t.Errorf("unsupported file admitted: %v", tc)
		}
	}
}
