package meta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"webdownld_go/internal/model"
)

// jsonBufPool 复用 JSON 序列化的 bytes.Buffer，降低 Raft 写入路径上的 GC 分配。
var jsonBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

type Service struct {
	raft *RaftClient // raft Raft 元数据客户端。
}

// NewService 创建元数据服务。
// raft 为底层一致性存储客户端。
func NewService(raft *RaftClient) *Service {
	s := new(Service)
	s.raft = raft
	return s
}

// NameHash 计算文件名哈希，用于名称级去重索引。
func NameHash(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name))))
	return hex.EncodeToString(sum[:])
}

// FileIDByOwnerAndName 基于所有者、文件名哈希与大小生成稳定文件 ID。
func FileIDByOwnerAndName(owner, name string, size int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", strings.TrimSpace(owner), NameHash(name), size)))
	return hex.EncodeToString(sum[:])
}

// UploadID 基于文件 ID 和当前时间生成上传会话 ID。
func UploadID(fileID string) string {
	sum := sha256.Sum256([]byte(fileID + ":" + time.Now().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:16])
}

// SaveUploadSession 保存上传会话元数据到 Raft。
// ss 为完整上传会话对象。
func (s *Service) SaveUploadSession(ctx context.Context, ss model.UploadSession) error {
	ss.UpdatedAt = time.Now()
	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer jsonBufPool.Put(buf)
	json.NewEncoder(buf).Encode(ss)
	return s.raft.Put(ctx, "upload:"+ss.UploadID, buf.String())
}

// GetUploadSession 按 uploadID 获取上传会话。
func (s *Service) GetUploadSession(ctx context.Context, uploadID string) (*model.UploadSession, error) {
	raw, err := s.raft.Get(ctx, "upload:"+uploadID)
	if err != nil {
		return nil, err
	}
	var ss model.UploadSession
	if err := json.Unmarshal([]byte(raw), &ss); err != nil {
		return nil, err
	}
	if ss.Received == nil {
		ss.Received = map[int]bool{}
	}
	if ss.ChunkMap == nil {
		ss.ChunkMap = map[int]string{}
	}
	items, err := s.raft.ListPrefix(ctx, uploadChunkPrefix(uploadID))
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		var ch model.UploadChunkState
		if json.Unmarshal([]byte(item.Value), &ch) == nil {
			ss.Received[ch.Index] = true
			ss.ChunkMap[ch.Index] = ch.ChunkHash + "|" + ch.StorageID + "|" + fmt.Sprintf("%d", ch.Size)
		}
	}
	return &ss, nil
}

// SaveUploadChunk 保存单个分片完成状态，避免并发上传时覆盖整个 UploadSession。
func (s *Service) SaveUploadChunk(ctx context.Context, uploadID string, ch model.UploadChunkState) error {
	ch.UpdatedAt = time.Now()
	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer jsonBufPool.Put(buf)
	json.NewEncoder(buf).Encode(ch)
	return s.raft.Put(ctx, uploadChunkKey(uploadID, ch.Index), buf.String())
}

// DeleteUploadSession 删除已完成上传的会话与分片状态。
func (s *Service) DeleteUploadSession(ctx context.Context, uploadID string) error {
	items, err := s.raft.ListPrefix(ctx, uploadChunkPrefix(uploadID))
	if err != nil {
		return err
	}
	var lastErr error
	for _, item := range items {
		if err := s.raft.Delete(ctx, item.Key); err != nil {
			lastErr = err
		}
	}
	if err := s.raft.Delete(ctx, "upload:"+uploadID); err != nil {
		lastErr = err
	}
	return lastErr
}

// SaveFile 保存文件元数据并更新名称索引与文件目录。
// fm 为完整文件元数据对象。
func (s *Service) SaveFile(ctx context.Context, fm model.FileMeta) error {
	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer jsonBufPool.Put(buf)
	json.NewEncoder(buf).Encode(fm)
	data := buf.String()
	if err := s.raft.Put(ctx, "file:"+fm.FileID, data); err != nil {
		return err
	}
	if err := s.raft.Put(ctx, ownerNameSizeIndexKey(fm.Owner, fm.NameHash, fm.Size), fm.FileID); err != nil {
		return err
	}
	return s.raft.Put(ctx, "catalog:file:"+fm.FileID, fm.FileID)
}

// GetFileByID 按文件 ID 查询文件元数据。
func (s *Service) GetFileByID(ctx context.Context, fileID string) (*model.FileMeta, error) {
	raw, err := s.raft.Get(ctx, "file:"+fileID)
	if err != nil {
		return nil, err
	}
	var fm model.FileMeta
	if err := json.Unmarshal([]byte(raw), &fm); err != nil {
		return nil, err
	}
	return &fm, nil
}

// GetFileByNameHash 按名称哈希查询文件元数据。
func (s *Service) GetFileByOwnerNameHashAndSize(ctx context.Context, owner, nameHash string, size int64) (*model.FileMeta, error) {
	fileID, err := s.raft.Get(ctx, ownerNameSizeIndexKey(owner, nameHash, size))
	if err != nil {
		return nil, err
	}
	return s.GetFileByID(ctx, strings.TrimSpace(fileID))
}

// ListFiles 列出目录中的全部文件元数据。
func (s *Service) ListFiles(ctx context.Context) ([]model.FileMeta, error) {
	items, err := s.raft.ListPrefix(ctx, "catalog:file:")
	if err != nil {
		return nil, err
	}
	out := make([]model.FileMeta, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.Value)
		if id == "" {
			continue
		}
		fm, err := s.GetFileByID(ctx, id)
		if err == nil && fm != nil {
			out = append(out, *fm)
		}
	}
	return out, nil
}

// ListFilesByOwner 列出指定用户拥有的全部文件。
func (s *Service) ListFilesByOwner(ctx context.Context, owner string) ([]model.FileMeta, error) {
	files, err := s.ListFiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.FileMeta, 0, len(files))
	for _, fm := range files {
		if fm.Owner == owner {
			out = append(out, fm)
		}
	}
	return out, nil
}

// ChunkReferencedByOtherFile 判断分片是否仍被目标文件之外的文件引用。
func (s *Service) ChunkReferencedByOtherFile(ctx context.Context, chunkHash, excludedFileID string) (bool, error) {
	files, err := s.ListFiles(ctx)
	if err != nil {
		return false, err
	}
	for _, fm := range files {
		if fm.FileID == excludedFileID {
			continue
		}
		for _, ch := range fm.Chunks {
			if ch.ChunkHash == chunkHash {
				return true, nil
			}
		}
	}
	return false, nil
}

// ChunkReferencedByUpload 判断分片是否仍被任意进行中的上传会话引用。
func (s *Service) ChunkReferencedByUpload(ctx context.Context, chunkHash string) (bool, error) {
	items, err := s.raft.ListPrefix(ctx, "uploadchunk:")
	if err != nil {
		return false, err
	}
	for _, item := range items {
		var ch model.UploadChunkState
		if json.Unmarshal([]byte(item.Value), &ch) == nil && ch.ChunkHash == chunkHash {
			return true, nil
		}
	}
	return false, nil
}

// DeleteFile 删除文件元数据、秒传索引和目录项（存储分片由调用方按引用情况清理）。
func (s *Service) DeleteFile(ctx context.Context, fm *model.FileMeta) error {
	// 顺序不重要，尽力删除。
	var lastErr error
	if err := s.raft.Delete(ctx, "file:"+fm.FileID); err != nil {
		lastErr = err
	}
	if err := s.raft.Delete(ctx, ownerNameSizeIndexKey(fm.Owner, fm.NameHash, fm.Size)); err != nil {
		lastErr = err
	}
	if err := s.raft.Delete(ctx, "catalog:file:"+fm.FileID); err != nil {
		lastErr = err
	}
	return lastErr
}

// nameSizeIndexKey 构造名称+大小全局索引键，用于秒传判定。
func ownerNameSizeIndexKey(owner, nameHash string, size int64) string {
	ownerHash := sha256.Sum256([]byte(strings.TrimSpace(owner)))
	return fmt.Sprintf("idx:ownername:%s:%s:%d", hex.EncodeToString(ownerHash[:]), nameHash, size)
}

// uploadChunkPrefix 构造上传分片键前缀，用于前缀扫描。
func uploadChunkPrefix(uploadID string) string {
	return "uploadchunk:" + uploadID + ":"
}

// uploadChunkKey 构造单个上传分片的完整键。
func uploadChunkKey(uploadID string, index int) string {
	return fmt.Sprintf("%s%06d", uploadChunkPrefix(uploadID), index)
}
