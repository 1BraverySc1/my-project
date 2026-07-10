package repository

import (
	"creator-platform/internal/model"

	"gorm.io/gorm"
)

type FileRepository struct {
	db *gorm.DB
}

func NewFileRepository(db *gorm.DB) *FileRepository {
	return &FileRepository{db: db}
}

func (r *FileRepository) Create(file *model.File) error {
	return r.db.Create(file).Error
}

func (r *FileRepository) FindByFileID(fileID string) (*model.File, error) {
	var file model.File
	err := r.db.Where("file_id = ?", fileID).First(&file).Error
	if err != nil {
		return nil, err
	}
	return &file, nil
}

func (r *FileRepository) FindByUserID(userID int64) ([]model.File, error) {
	var files []model.File
	err := r.db.Where("user_id = ? AND status = 1", userID).Order("created_at DESC").Find(&files).Error
	return files, err
}

func (r *FileRepository) FindByContentHash(hash string) (*model.File, error) {
	var file model.File
	err := r.db.Where("content_hash = ? AND status = 1", hash).First(&file).Error
	if err != nil {
		return nil, err
	}
	return &file, nil
}

func (r *FileRepository) Delete(id int64, userID int64) error {
	return r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&model.File{}).Error
}
