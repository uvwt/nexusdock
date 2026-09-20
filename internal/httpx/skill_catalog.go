package httpx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/nexusdock/internal/skillcatalog"
)

type skillDownloadTicket struct {
	Name, Version string
	Expires       time.Time
}
type skillCatalogService struct {
	store   *skillcatalog.Store
	mu      sync.Mutex
	tickets map[string]skillDownloadTicket
}

func (s *Server) registerSkillCatalogRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/skill-catalog", protected(s.listSkillCatalog))
	mux.HandleFunc("POST /v1/skill-catalog", protected(s.uploadSkillCatalog))
	mux.HandleFunc("GET /v1/skill-catalog/{name}/{version}", protected(s.getSkillCatalog))
	mux.HandleFunc("PUT /v1/skill-catalog/{name}/{version}", protected(s.reviewSkillCatalog))
	mux.HandleFunc("GET /v1/skill-catalog/{name}/{version}/download", protected(s.downloadSkillCatalog))
	mux.HandleFunc("GET /v1/skill-catalog/{name}/{version}/files/{filePath...}", protected(s.readSkillCatalogFile))
	mux.HandleFunc("POST /v1/skill-catalog/{name}/{version}/nodes/{nodeID}", protected(s.distributeSkillCatalog))
	// 节点无需管理员会话；短时票据仅授权本次校验/安装读取一个不可变包。
	mux.HandleFunc("GET /skill-packages/download", s.downloadSkillTicket)
}

func (s *Server) listSkillCatalog(w http.ResponseWriter, r *http.Request) {
	items, err := s.skillCatalog.store.List()
	if err != nil {
		writeError(w, 500, "SKILL_CATALOG_FAILED", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": items})
}
func (s *Server) uploadSkillCatalog(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, skillcatalog.MaxArchive+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, 400, "SKILL_PACKAGE_INVALID", "invalid or oversized multipart upload")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, _, err := r.FormFile("package")
	if err != nil {
		writeError(w, 400, "SKILL_PACKAGE_INVALID", "package file is required")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, skillcatalog.MaxArchive+1))
	if err != nil {
		writeError(w, 400, "SKILL_PACKAGE_INVALID", err.Error())
		return
	}
	var meta skillcatalog.Metadata
	if raw := r.FormValue("metadata"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &meta); err != nil {
			writeError(w, 400, "SKILL_PACKAGE_INVALID", "invalid metadata")
			return
		}
	}
	e, err := s.skillCatalog.store.Put(data, meta)
	if err != nil {
		status := 400
		if errors.Is(err, skillcatalog.ErrConflict) {
			status = 409
		}
		writeError(w, status, "SKILL_PACKAGE_INVALID", err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "entry": e})
}
func (s *Server) getSkillCatalog(w http.ResponseWriter, r *http.Request) {
	e, err := s.skillCatalog.store.Get(r.PathValue("name"), r.PathValue("version"))
	if err != nil {
		writeError(w, 404, "SKILL_PACKAGE_NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "entry": e})
}

func (s *Server) reviewSkillCatalog(w http.ResponseWriter, r *http.Request) {
	var m skillcatalog.Metadata
	if !decodeJSON(w, r, &m) {
		return
	}
	e, err := s.skillCatalog.store.Review(r.PathValue("name"), r.PathValue("version"), m)
	if err != nil {
		writeError(w, 400, "SKILL_PACKAGE_INVALID", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "entry": e})
}

func (s *Server) readSkillCatalogFile(w http.ResponseWriter, r *http.Request) {
	content, err := s.skillCatalog.store.ReadFile(r.PathValue("name"), r.PathValue("version"), r.PathValue("filePath"))
	if err != nil {
		writeError(w, 400, "SKILL_PACKAGE_INVALID", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "content": content})
}
func (s *Server) serveSkillPackage(w http.ResponseWriter, r *http.Request, name, version string) {
	e, b, err := s.skillCatalog.store.Archive(name, version)
	if err != nil {
		writeError(w, 404, "SKILL_PACKAGE_NOT_FOUND", err.Error())
		return
	}
	ext := ".tar.gz"
	if bytes.HasPrefix(b, []byte("PK")) {
		ext = ".zip"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s%s"`, e.Name, e.Version, ext))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", `"`+e.SHA256+`"`)
	http.ServeContent(w, r, e.Name+ext, e.CreatedAt, bytes.NewReader(b))
}
func (s *Server) downloadSkillCatalog(w http.ResponseWriter, r *http.Request) {
	s.serveSkillPackage(w, r, r.PathValue("name"), r.PathValue("version"))
}
func (s *Server) downloadSkillTicket(w http.ResponseWriter, r *http.Request) {
	s.skillCatalog.mu.Lock()
	ticket, ok := s.skillCatalog.tickets[r.URL.Query().Get("ticket")]
	s.skillCatalog.mu.Unlock()
	if !ok || time.Now().After(ticket.Expires) {
		writeError(w, 403, "SKILL_DOWNLOAD_EXPIRED", "invalid or expired package download")
		return
	}
	e, b, err := s.skillCatalog.store.InstallArchive(ticket.Name, ticket.Version)
	if err != nil {
		writeError(w, 404, "SKILL_PACKAGE_NOT_FOUND", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, e.Name+".zip", e.CreatedAt, bytes.NewReader(b))
}

type catalogNodeRequest struct {
	Action string `json:"action"`
	Digest string `json:"digest,omitempty"`
}
type catalogValidation struct {
	Valid  bool   `json:"valid"`
	Digest string `json:"digest"`
}

// 所有节点操作由已有 MCP 契约和反向 Hub 执行；不连接节点公网端口。
func (s *Server) catalogTool(ctx context.Context, node string, args map[string]any) (json.RawMessage, error) {
	args["node_id"] = node
	result, err := s.callNodeTool(ctx, "skill_package", args)
	if err != nil {
		return nil, err
	}
	if result == nil || result.IsError {
		return nil, errors.New("node rejected Skill package operation; inspect node logs")
	}
	b, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" {
		return nil, errors.New("node returned no structured package result")
	}
	return b, nil
}
func (s *Server) distributeSkillCatalog(w http.ResponseWriter, r *http.Request) {
	var request catalogNodeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Action != "validate" && request.Action != "install" {
		writeError(w, 400, "SKILL_PACKAGE_INVALID", "action must be validate or install")
		return
	}
	entry, err := s.skillCatalog.store.Get(r.PathValue("name"), r.PathValue("version"))
	if err != nil {
		writeError(w, 404, "SKILL_PACKAGE_NOT_FOUND", err.Error())
		return
	}
	if s.agentDock == nil || s.agentDockHub == nil {
		writeError(w, 503, "SKILL_DISTRIBUTION_FAILED", "node service unavailable")
		return
	}
	if request.Action == "install" && entry.Metadata.Portability == "unreviewed" {
		writeError(w, 409, "SKILL_DISTRIBUTION_FAILED", "review portability and usage before installation")
		return
	}
	nodeID := r.PathValue("nodeID")
	node, err := s.agentDock.Get(r.Context(), nodeID)
	if err != nil {
		writeRuntimeUnavailable(w, err)
		return
	}
	if !node.Enabled || !s.agentDockHub.Online(nodeID) {
		writeError(w, 409, "SKILL_DISTRIBUTION_FAILED", "target node is offline or disabled")
		return
	}
	if len(entry.Metadata.Platforms) > 0 {
		match := false
		for _, p := range entry.Metadata.Platforms {
			if p == node.OS {
				match = true
			}
		}
		if !match {
			writeError(w, 409, "SKILL_DISTRIBUTION_FAILED", "package does not support target platform")
			return
		}
	}
	if s.cfg.PublicURL == "" {
		writeError(w, 503, "SKILL_DISTRIBUTION_FAILED", "configure NEXUS_PUBLIC_URL so nodes can download packages")
		return
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		writeError(w, 500, "SKILL_DISTRIBUTION_FAILED", "cannot issue package ticket")
		return
	}
	token := hex.EncodeToString(nonce[:])
	s.skillCatalog.mu.Lock()
	for k, v := range s.skillCatalog.tickets {
		if time.Now().After(v.Expires) {
			delete(s.skillCatalog.tickets, k)
		}
	}
	s.skillCatalog.tickets[token] = skillDownloadTicket{entry.Name, entry.Version, time.Now().Add(5 * time.Minute)}
	s.skillCatalog.mu.Unlock()
	defer func() { s.skillCatalog.mu.Lock(); delete(s.skillCatalog.tickets, token); s.skillCatalog.mu.Unlock() }()
	source := strings.TrimRight(s.cfg.PublicURL, "/") + "/skill-packages/download?ticket=" + url.QueryEscape(token)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	validated, err := s.catalogTool(ctx, nodeID, map[string]any{"action": "validate", "source": source, "max_bytes": skillcatalog.MaxArchive})
	if err != nil {
		writeError(w, 502, "SKILL_DISTRIBUTION_FAILED", err.Error())
		return
	}
	var validation catalogValidation
	if err := json.Unmarshal(validated, &validation); err != nil || !validation.Valid || validation.Digest != "sha256:"+entry.InstallSHA256 {
		writeJSON(w, 422, map[string]any{"ok": false, "status": "validation_failed", "validation": validated})
		return
	}
	if request.Action == "validate" {
		writeJSON(w, 200, map[string]any{"ok": true, "status": "validated", "digest": validation.Digest, "archive_sha256": entry.SHA256})
		return
	}
	// 安装必须引用用户刚审阅的节点校验摘要，并在每次安装前重新校验。
	if request.Digest == "" || request.Digest != validation.Digest {
		writeError(w, 409, "SKILL_DISTRIBUTION_FAILED", "validate this package on this node before installing; digest mismatch")
		return
	}
	installed, err := s.catalogTool(ctx, nodeID, map[string]any{"action": "install", "source": source, "digest": validation.Digest, "max_bytes": skillcatalog.MaxArchive, "activate": true})
	if err != nil {
		writeError(w, 502, "SKILL_DISTRIBUTION_FAILED", err.Error())
		return
	}
	detail, _, err := s.agentDockHub.RuntimeSkill(ctx, nodeID, entry.Name)
	if err != nil || detail.ActiveVersion != entry.Version {
		writeJSON(w, 202, map[string]any{"ok": true, "status": "installation_unverified", "installation": installed})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "status": "installed_dependencies_unverified", "version": detail.ActiveVersion, "digest": validation.Digest})
}

func newSkillCatalog(dataDir string) *skillCatalogService {
	// 初始化不写磁盘；无配置的测试服务器也不会意外生成工作目录。
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "nexus-unconfigured")
	}
	return &skillCatalogService{store: skillcatalog.New(filepath.Join(dataDir, "skill-catalog")), tickets: map[string]skillDownloadTicket{}}
}
