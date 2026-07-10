package repository

import (
	"creator-platform/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UploadRepository struct {
	db *gorm.DB
}

func NewUploadRepository(db *gorm.DB) *UploadRepository {
	return &UploadRepository{db: db}
}

func (r *UploadRepository) Create(session *model.UploadSession) error {
	return r.db.Create(session).Error
}

func (r *UploadRepository) FindByUploadID(uploadID string) (*model.UploadSession, error) {
	var session model.UploadSession
	err := r.db.Where("upload_id = ?", uploadID).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *UploadRepository) UpdateReceivedChunks(uploadID string, receivedJSON string) error {
	return r.db.Model(&model.UploadSession{}).
		Where("upload_id = ?", uploadID).
		Update("received_chunks", receivedJSON).Error
}

func (r *UploadRepository) UpdateStatus(uploadID string, status int) error {
	return r.db.Model(&model.UploadSession{}).
		Where("upload_id = ?", uploadID).
		Update("status", status).Error
}

func (r *UploadRepository) Delete(uploadID string) error {
	return r.db.Where("upload_id = ?", uploadID).Delete(&model.UploadSession{}).Error
}
