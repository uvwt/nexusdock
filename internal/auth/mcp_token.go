package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const mcpAccessTokenBytes = 32

type MCPTokenStore struct {
	mu    sync.RWMutex
	path  string
	token string
}

func NewMCPTokenStore(dataDir string) (*MCPTokenStore, error) {
	path := filepath.Join(dataDir, "secrets", "mcp-access-token")
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建 MCP Token 目录: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("收紧 MCP Token 目录权限: %w", err)
	}
	if token, err := readMCPToken(path); err == nil {
		return &MCPTokenStore{path: path, token: token}, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	token, err := generateMCPToken()
	if err != nil {
		return nil, err
	}
	if err := writeInitialMCPToken(path, token); err != nil {
		if os.IsExist(err) {
			token, err = readMCPToken(path)
			if err == nil {
				return &MCPTokenStore{path: path, token: token}, nil
			}
		}
		return nil, err
	}
	return &MCPTokenStore{path: path, token: token}, nil
}

func (s *MCPTokenStore) Token() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

func (s *MCPTokenStore) Reset() (string, error) {
	if s == nil {
		return "", fmt.Errorf("MCP Token 存储不可用")
	}
	token, err := generateMCPToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// 重置必须先把新 Token 完整落盘，再切换内存值，避免写盘失败时出现
	// “旧 Token 已失效但新 Token 未持久化”的半完成状态。
	if err := replaceMCPToken(s.path, token); err != nil {
		return "", err
	}
	s.token = token
	return token, nil
}

func readMCPToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("MCP Token 文件不能是符号链接")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if len(token) != mcpAccessTokenBytes*2 {
		return "", fmt.Errorf("MCP Token 文件格式无效")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", fmt.Errorf("MCP Token 文件格式无效")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("收紧 MCP Token 文件权限: %w", err)
	}
	return token, nil
}

func generateMCPToken() (string, error) {
	value := make([]byte, mcpAccessTokenBytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("生成 MCP Token: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func writeInitialMCPToken(path, token string) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".mcp-access-token-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.WriteString(token + "\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("写入 MCP Token: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("设置 MCP Token 文件权限: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("同步 MCP Token 文件: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭 MCP Token 文件: %w", err)
	}
	// hard link 只在目标不存在时成功，既保留并发首次创建的 O_EXCL 语义，
	// 又确保最终路径只会指向已经完整 fsync 的文件。
	if err := os.Link(tempPath, path); err != nil {
		return err
	}
	syncTokenDirectory(dir)
	return nil
}

func replaceMCPToken(path, token string) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".mcp-access-token-*")
	if err != nil {
		return fmt.Errorf("创建 MCP Token 临时文件: %w", err)
	}
	tempPath := file.Name()
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("设置 MCP Token 临时文件权限: %w", err)
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		return fmt.Errorf("写入 MCP Token 临时文件: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步 MCP Token 临时文件: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭 MCP Token 临时文件: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("替换 MCP Token 文件: %w", err)
	}
	cleanup = false

	// 目录同步保证 rename 元数据也持久化；部分文件系统不支持目录 Sync，
	// 这种情况下 Token 文件已经原子替换完成，不应把成功重置回滚成失败。
	syncTokenDirectory(dir)
	return nil
}

func syncTokenDirectory(dir string) {
	dirFile, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = dirFile.Sync()
	_ = dirFile.Close()
}
