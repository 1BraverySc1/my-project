package model

import "time"

type User struct {
	ID           int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string    `gorm:"uniqueIndex;size:64;not null" json:"username"`
	PasswordHash string    `gorm:"size:256;not null" json:"-"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (User) TableName() string { return "users" }
