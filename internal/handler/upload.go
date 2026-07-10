package handler

import (
	"io"
	"net/http"
	"strconv"

	"creator-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type UploadHandler struct {
	uploadService *service.UploadService
}

func NewUploadHandler(uploadService *service.UploadService) *UploadHandler {
	return &UploadHandler{uploadService: uploadService}
}

func (h *UploadHandler) Register(r *gin.RouterGroup) {
	r.POST("/uploads/init", h.initUpload)
	r.GET("/uploads/:id/status", h.uploadStatus)
	r.POST("/uploads/:id/chunks/:index", h.uploadChunk)
	r.POST("/uploads/:id/complete", h.completeUpload)
	r.GET("/files", h.listFiles)
	r.GET("/files/:id/download", h.downloadFile)
	r.DELETE("/files/:id", h.deleteFile)
}

type initReq struct {
	Name   string `json:"name" binding:"required"`
	Size   int64  `json:"size" binding:"required,gt=0"`
	Hash   string `json:"hash" binding:"required"`
}

func (h *UploadHandler) initUpload(c *gin.Context) {
	var req initReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	userID := c.GetInt64("user_id")
	result, err := h.uploadService.InitUpload(userID, req.Name, req.Size, req.Hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "data": result})
}

func (h *UploadHandler) uploadStatus(c *gin.Context) {
	uploadID := c.Param("id")

	received, total, err := h.uploadService.GetUploadStatus(uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "上传会话不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"received":     received,
		"total_chunks": total,
	})
}

func (h *UploadHandler) uploadChunk(c *gin.Context) {
	uploadID := c.Param("id")
	indexStr := c.Param("index")

	chunkIndex, err := strconv.Atoi(indexStr)
	if err != nil || chunkIndex < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分片索引无效"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取分片数据失败"})
		return
	}
	defer c.Request.Body.Close()

	if err := h.uploadService.SaveChunk(uploadID, chunkIndex, body); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *UploadHandler) completeUpload(c *gin.Context) {
	uploadID := c.Param("id")
	userID := c.GetInt64("user_id")

	file, err := h.uploadService.CompleteUpload(uploadID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "data": file})
}

func (h *UploadHandler) listFiles(c *gin.Context) {
	userID := c.GetInt64("user_id")
	files, err := h.uploadService.ListFiles(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "data": files})
}

func (h *UploadHandler) downloadFile(c *gin.Context) {
	fileID := c.Param("id")
	userID := c.GetInt64("user_id")

	filePath, err := h.uploadService.GetFilePath(fileID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	file, _ := h.uploadService.ListFiles(userID)
	for _, f := range file {
		if f.FileID == fileID {
			c.Header("Content-Disposition", "attachment; filename="+f.Name)
			c.File(filePath)
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
}

func (h *UploadHandler) deleteFile(c *gin.Context) {
	fileID := c.Param("id")
	userID := c.GetInt64("user_id")

	if err := h.uploadService.DeleteFile(fileID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}
