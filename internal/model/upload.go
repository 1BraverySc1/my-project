package model

import "time"

type UploadSession struct {
	UploadID       string    `gorm:"primaryKey;size:64" json:"upload_id"`
	FileID         string    `gorm:"index;size:64;not null" json:"file_id"`
	UserID         int64     `gorm:"index;not null" json:"user_id"`
	FileName       string    `gorm:"size:512;not null" json:"file_name"`
	FileSize       int64     `gorm:"not null" json:"file_size"`
	ContentHash    string    `gorm:"size:64;not null" json:"content_hash"`
	ChunkSize      int       `gorm:"not null" json:"chunk_size"`
	TotalChunks    int       `gorm:"not null" json:"total_chunks"`
	ReceivedChunks string    `gorm:"type:text" json:"received_chunks"`
	Status         int       `gorm:"default:0" json:"status"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (UploadSession) TableName() string { return "upload_sessions" }
