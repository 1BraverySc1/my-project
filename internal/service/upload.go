package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"creator-platform/internal/config"
	"creator-platform/internal/model"
	"creator-platform/internal/repository"

	"github.com/redis/go-redis/v9"
)

type UploadService struct {
	fileRepo   *repository.FileRepository
	uploadRepo *repository.UploadRepository
	rdb        *redis.Client
	cfg        *config.UploadConfig
	mu         sync.Mutex
}

func NewUploadService(fileRepo *repository.FileRepository, uploadRepo *repository.UploadRepository, rdb *redis.Client, cfg *config.UploadConfig) *UploadService {
	return &UploadService{
		fileRepo:   fileRepo,
		uploadRepo: uploadRepo,
		rdb:        rdb,
		cfg:        cfg,
	}
}

// ContentHash 计算文件内容 SHA256
func ContentHash(reader io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

type InitUploadResult struct {
	UploadID      string `json:"upload_id"`
	FileID        string `json:"file_id"`
	ChunkSize     int    `json:"chunk_size"`
	TotalChunks   int    `json:"total_chunks"`
	InstantUpload bool   `json:"instant_upload"`
}

func (s *UploadService) InitUpload(userID int64, fileName string, fileSize int64, contentHash string) (*InitUploadResult, error) {
	// 查秒传
	if existing, err := s.fileRepo.FindByContentHash(contentHash); err == nil && existing != nil {
		return &InitUploadResult{
			FileID:        existing.FileID,
			ChunkSize:     existing.ChunkSize,
			TotalChunks:   existing.TotalChunks,
			InstantUpload: true,
		}, nil
	}

	chunkSize := s.cfg.ChunkSize
	totalChunks := int((fileSize + int64(chunkSize) - 1) / int64(chunkSize))

	// 生成 upload_id
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%d", userID, fileName, time.Now().UnixNano())))
	uploadID := hex.EncodeToString(hash[:16])
	fileID := contentHash[:32]

	session := &model.UploadSession{
		UploadID:       uploadID,
		FileID:         fileID,
		UserID:         userID,
		FileName:       fileName,
		FileSize:       fileSize,
		ContentHash:    contentHash,
		ChunkSize:      chunkSize,
		TotalChunks:    totalChunks,
		ReceivedChunks: "[]",
		Status:         0,
	}

	if err := s.uploadRepo.Create(session); err != nil {
		return nil, err
	}

	return &InitUploadResult{
		UploadID:    uploadID,
		FileID:      fileID,
		ChunkSize:   chunkSize,
		TotalChunks: totalChunks,
	}, nil
}

func (s *UploadService) GetUploadStatus(uploadID string) ([]int, int, error) {
	session, err := s.uploadRepo.FindByUploadID(uploadID)
	if err != nil {
		return nil, 0, err
	}

	var received []int
	json.Unmarshal([]byte(session.ReceivedChunks), &received)
	return received, session.TotalChunks, nil
}

func (s *UploadService) SaveChunk(uploadID string, chunkIndex int, data []byte) error {
	session, err := s.uploadRepo.FindByUploadID(uploadID)
	if err != nil {
		return err
	}

	// 校验分片索引
	if chunkIndex < 0 || chunkIndex >= session.TotalChunks {
		return errors.New("分片索引无效")
	}

	// 写入文件
	chunkDir := filepath.Join(s.cfg.DataDir, "chunks", uploadID)
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		return err
	}

	chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%06d", chunkIndex))
	if err := os.WriteFile(chunkPath, data, 0644); err != nil {
		return err
	}

	// 更新已接收分片列表
	var received []int
	json.Unmarshal([]byte(session.ReceivedChunks), &received)

	// 去重
	for _, v := range received {
		if v == chunkIndex {
			return nil
		}
	}

	received = append(received, chunkIndex)
	receivedJSON, _ := json.Marshal(received)
	return s.uploadRepo.UpdateReceivedChunks(uploadID, string(receivedJSON))
}

func (s *UploadService) CompleteUpload(uploadID string, userID int64) (*model.File, error) {
	session, err := s.uploadRepo.FindByUploadID(uploadID)
	if err != nil {
		return nil, err
	}

	if session.UserID != userID {
		return nil, errors.New("无权操作")
	}

	var received []int
	json.Unmarshal([]byte(session.ReceivedChunks), &received)

	if len(received) != session.TotalChunks {
		return nil, fmt.Errorf("分片未完整上传: %d/%d", len(received), session.TotalChunks)
	}

	// 合并分片
	mergePath := filepath.Join(s.cfg.DataDir, "files", session.FileID)
	if err := os.MkdirAll(filepath.Dir(mergePath), 0755); err != nil {
		return nil, err
	}

	dst, err := os.Create(mergePath)
	if err != nil {
		return nil, err
	}
	defer dst.Close()

	chunkDir := filepath.Join(s.cfg.DataDir, "chunks", uploadID)
	for i := 0; i < session.TotalChunks; i++ {
		chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%06d", i))
		src, err := os.Open(chunkPath)
		if err != nil {
			return nil, err
		}
		io.Copy(dst, src)
		src.Close()
		os.Remove(chunkPath)
	}

	// 清理临时目录
	os.RemoveAll(chunkDir)

	// 验证合并后文件哈希
	dst.Close()
	f, _ := os.Open(mergePath)
	actualHash, _, _ := ContentHash(f)
	f.Close()

	if actualHash != session.ContentHash {
		os.Remove(mergePath)
		return nil, errors.New("文件完整性校验失败")
	}

	// 保存文件元数据
	file := &model.File{
		FileID:      session.FileID,
		Name:        session.FileName,
		Size:        session.FileSize,
		ChunkSize:   session.ChunkSize,
		TotalChunks: session.TotalChunks,
		ContentHash: session.ContentHash,
		UserID:      userID,
		Status:      1,
		FilePath:    mergePath,
	}

	if err := s.fileRepo.Create(file); err != nil {
		return nil, err
	}

	// 清理上传会话
	s.uploadRepo.Delete(uploadID)

	return file, nil
}

func (s *UploadService) ListFiles(userID int64) ([]model.File, error) {
	return s.fileRepo.FindByUserID(userID)
}

func (s *UploadService) DeleteFile(fileID string, userID int64) error {
	file, err := s.fileRepo.FindByFileID(fileID)
	if err != nil {
		return err
	}

	if file.UserID != userID {
		return errors.New("无权操作")
	}

	os.Remove(file.FilePath)
	return s.fileRepo.Delete(file.ID, userID)
}

func (s *UploadService) GetFilePath(fileID string, userID int64) (string, error) {
	file, err := s.fileRepo.FindByFileID(fileID)
	if err != nil {
		return "", err
	}

	if file.UserID != userID {
		return "", errors.New("无权操作")
	}

	return file.FilePath, nil
}
