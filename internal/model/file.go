package model

import "time"

type File struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	FileID      string    `gorm:"uniqueIndex:idx_file_id;size:64;not null" json:"file_id"`
	Name        string    `gorm:"size:512;not null" json:"name"`
	Size        int64     `gorm:"not null" json:"size"`
	ChunkSize   int       `gorm:"not null" json:"chunk_size"`
	TotalChunks int       `gorm:"not null" json:"total_chunks"`
	ContentHash string    `gorm:"uniqueIndex:idx_content_hash;size:64;not null" json:"content_hash"`
	UserID      int64     `gorm:"index;not null" json:"user_id"`
	Status      int       `gorm:"default:0" json:"status"`
	FilePath    string    `gorm:"size:1024" json:"-"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (File) TableName() string { return "files" }
