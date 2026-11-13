package models

// ImageSourceAlias is a private, user-declared source fingerprint. Only the
// canonical FileObject SHA256 describes bytes verified by the server.
type ImageSourceAlias struct {
	BaseModel
	UserID             uint   `gorm:"not null;uniqueIndex:idx_image_source_alias,priority:1"`
	SourceSHA256       string `gorm:"size:64;not null;uniqueIndex:idx_image_source_alias,priority:2"`
	SourceSizeBytes    int64  `gorm:"not null;uniqueIndex:idx_image_source_alias,priority:3"`
	ImagePolicyVersion string `gorm:"size:96;not null;uniqueIndex:idx_image_source_alias,priority:4"`
	FileID             string `gorm:"size:64;not null;index"`
}

func (ImageSourceAlias) TableName() string { return "file_image_source_aliases" }
