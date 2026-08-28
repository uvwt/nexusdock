package httpx

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/agentdock"
)

const (
	maxProxiedArtifactBytes               = 512 << 20
	artifactChunkTimeout                  = 30 * time.Second
	maxArtifactChunkRequests              = (maxProxiedArtifactBytes + protocol.MaxArtifactChunkBytes - 1) / protocol.MaxArtifactChunkBytes
	maxConcurrentArtifactDownloadsPerNode = 2
	// The overall deadline intentionally bounds slow or adversarial nodes even when
	// every individual chunk remains below artifactChunkTimeout.
	artifactDownloadTimeout = 30 * time.Minute
)

func (s *Server) decorateArtifactToolResult(nodeID string, envelope map[string]any) error {
	if strings.TrimSpace(s.cfg.PublicURL) == "" || envelope == nil {
		return nil
	}
	structured, ok := envelope["structuredContent"].(map[string]any)
	if !ok {
		return nil
	}
	artifactID, _ := structured["artifact_id"].(string)
	filename, _ := structured["filename"].(string)
	sha, _ := structured["sha256"].(string)
	sha = strings.ToLower(strings.TrimSpace(sha))
	expiresText, _ := structured["expires_at"].(string)
	if artifactID == "" || filename == "" || !validArtifactSHA(sha) || expiresText == "" {
		return nil
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expiresText)
	if err != nil || !expiresAt.After(time.Now().UTC()) {
		return nil
	}
	publicURL, err := s.signedArtifactURL(nodeID, artifactID, filename, sha, expiresAt.Unix())
	if err != nil {
		return err
	}
	structured["url"] = publicURL
	structured["download_via"] = "nexusdock"
	refreshEnvelopeTextContent(envelope, structured)
	return nil
}

func refreshEnvelopeTextContent(envelope, structured map[string]any) {
	content, ok := envelope["content"].([]any)
	if !ok {
		return
	}
	for _, item := range content {
		block, ok := item.(map[string]any)
		if ok && block["type"] == "text" {
			block["text"] = prettyJSON(structured)
			return
		}
	}
}

func (s *Server) signedArtifactURL(nodeID, artifactID, filename, sha string, expires int64) (string, error) {
	secret, err := s.artifactSigningSecret()
	if err != nil {
		return "", err
	}
	signature := signArtifactURL(secret, nodeID, artifactID, filename, sha, expires)
	query := url.Values{
		"expires": {strconv.FormatInt(expires, 10)},
		"sha256":  {sha},
		"sig":     {signature},
	}
	base := strings.TrimRight(strings.TrimSpace(s.cfg.PublicURL), "/")
	return base + "/artifacts/public/" + url.PathEscape(nodeID) + "/" + url.PathEscape(artifactID) + "/" + url.PathEscape(filename) + "?" + query.Encode(), nil
}

func (s *Server) servePublicArtifact(w http.ResponseWriter, r *http.Request) {
	setArtifactPublicHeaders(w.Header())
	if s.agentDockHub == nil {
		http.NotFound(w, r)
		return
	}
	nodeID := strings.TrimSpace(r.PathValue("nodeID"))
	artifactID := strings.TrimSpace(r.PathValue("artifactID"))
	filename := strings.TrimSpace(r.PathValue("filename"))
	expires, parseErr := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	sha := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sha256")))
	signature := strings.TrimSpace(r.URL.Query().Get("sig"))
	if nodeID == "" || artifactID == "" || filename == "" || parseErr != nil || expires <= 0 || !validArtifactSHA(sha) || signature == "" {
		http.NotFound(w, r)
		return
	}
	if time.Now().UTC().Unix() > expires {
		http.Error(w, http.StatusText(http.StatusGone), http.StatusGone)
		return
	}
	secret, err := s.artifactSigningSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ARTIFACT_SECRET_FAILED", "Artifact download is temporarily unavailable")
		return
	}
	expected := signArtifactURL(secret, nodeID, artifactID, filename, sha, expires)
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		if !s.acquireArtifactDownload(nodeID) {
			w.Header().Set("Retry-After", "5")
			writeError(w, http.StatusTooManyRequests, "ARTIFACT_DOWNLOAD_BUSY", "Too many concurrent Artifact downloads for this node")
			return
		}
		defer s.releaseArtifactDownload(nodeID)
	}

	downloadCtx, cancel := context.WithTimeout(r.Context(), artifactDownloadTimeout)
	defer cancel()

	if r.Method == http.MethodHead {
		chunk, readErr := s.readArtifactChunk(downloadCtx, nodeID, artifactID, 0, 0)
		if readErr != nil {
			s.writeArtifactBridgeError(w, readErr)
			return
		}
		if err := validateArtifactChunk(chunk, artifactID, filename, sha, expires, 0, true); err != nil {
			writeError(w, http.StatusBadGateway, "ARTIFACT_NODE_RESPONSE_INVALID", err.Error())
			return
		}
		if chunk.Size > maxProxiedArtifactBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "ARTIFACT_TOO_LARGE", "Artifact exceeds the NexusDock proxy limit")
			return
		}
		setArtifactHeaders(w.Header(), chunk)
		w.WriteHeader(http.StatusOK)
		return
	}

	first, err := s.readArtifactChunk(downloadCtx, nodeID, artifactID, 0, protocol.MaxArtifactChunkBytes)
	if err != nil {
		s.writeArtifactBridgeError(w, err)
		return
	}
	if err := validateArtifactChunk(first, artifactID, filename, sha, expires, 0, false); err != nil {
		writeError(w, http.StatusBadGateway, "ARTIFACT_NODE_RESPONSE_INVALID", err.Error())
		return
	}
	if first.Size > maxProxiedArtifactBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "ARTIFACT_TOO_LARGE", "Artifact exceeds the NexusDock proxy limit")
		return
	}
	firstData, err := decodeArtifactChunkData(first, 0)
	if err != nil {
		writeError(w, http.StatusBadGateway, "ARTIFACT_NODE_RESPONSE_INVALID", err.Error())
		return
	}
	setArtifactHeaders(w.Header(), first)
	w.WriteHeader(http.StatusOK)

	hasher := sha256.New()
	_, _ = hasher.Write(firstData)
	pending := firstData
	offset := first.NextOffset
	chunk := first
	chunkRequests := 1
	for !chunk.EOF {
		if chunkRequests >= maxArtifactChunkRequests {
			s.artifactLogger().Error("AgentDock Artifact stream exceeded chunk request limit", "node_id", nodeID, "artifact_id", artifactID, "requests", chunkRequests)
			panic(http.ErrAbortHandler)
		}
		chunk, err = s.readArtifactChunk(downloadCtx, nodeID, artifactID, offset, protocol.MaxArtifactChunkBytes)
		chunkRequests++
		if err != nil {
			s.artifactLogger().Error("read AgentDock Artifact chunk", "node_id", nodeID, "artifact_id", artifactID, "offset", offset, "error", err)
			panic(http.ErrAbortHandler)
		}
		if chunk.Size != first.Size {
			s.artifactLogger().Error("AgentDock Artifact size changed between chunks", "node_id", nodeID, "artifact_id", artifactID, "offset", offset, "first_size", first.Size, "chunk_size", chunk.Size)
			panic(http.ErrAbortHandler)
		}
		if err := validateArtifactChunk(chunk, artifactID, filename, sha, expires, offset, false); err != nil {
			s.artifactLogger().Error("validate AgentDock Artifact chunk", "node_id", nodeID, "artifact_id", artifactID, "offset", offset, "error", err)
			panic(http.ErrAbortHandler)
		}
		data, decodeErr := decodeArtifactChunkData(chunk, offset)
		if decodeErr != nil {
			s.artifactLogger().Error("decode AgentDock Artifact chunk", "node_id", nodeID, "artifact_id", artifactID, "offset", offset, "error", decodeErr)
			panic(http.ErrAbortHandler)
		}
		_, _ = hasher.Write(data)
		if len(pending) > 0 {
			if _, err := w.Write(pending); err != nil {
				return
			}
		}
		pending = data
		offset = chunk.NextOffset
	}
	if offset != first.Size || hex.EncodeToString(hasher.Sum(nil)) != sha {
		s.artifactLogger().Error("AgentDock Artifact stream checksum mismatch", "node_id", nodeID, "artifact_id", artifactID, "bytes", offset)
		panic(http.ErrAbortHandler)
	}
	if len(pending) > 0 {
		_, _ = w.Write(pending)
	}
}

func (s *Server) readArtifactChunk(ctx context.Context, nodeID, artifactID string, offset int64, maxBytes int) (agentdock.ArtifactChunk, error) {
	chunkCtx, cancel := context.WithTimeout(ctx, artifactChunkTimeout)
	defer cancel()
	return s.agentDockHub.ReadArtifactChunk(chunkCtx, nodeID, artifactID, offset, maxBytes)
}

func decodeArtifactChunkData(chunk agentdock.ArtifactChunk, offset int64) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
	if err != nil {
		return nil, errors.New("AgentDock Artifact chunk payload is not valid base64")
	}
	if len(data) > protocol.MaxArtifactChunkBytes || chunk.NextOffset != offset+int64(len(data)) {
		return nil, errors.New("AgentDock Artifact chunk payload length does not match its offsets")
	}
	return data, nil
}

func validateArtifactChunk(chunk agentdock.ArtifactChunk, artifactID, filename, sha string, expires, offset int64, metadataOnly bool) error {
	if chunk.ArtifactID != artifactID || chunk.Filename != filename || strings.ToLower(chunk.SHA256) != sha || chunk.ExpiresAt.Unix() != expires {
		return errors.New("AgentDock Artifact metadata does not match the signed URL")
	}
	if chunk.Size < 0 || chunk.Offset != offset {
		return errors.New("AgentDock Artifact size or offset is invalid")
	}
	if chunk.NextOffset < chunk.Offset || chunk.NextOffset > chunk.Size {
		return errors.New("AgentDock Artifact next offset is invalid")
	}
	if !metadataOnly && !chunk.EOF && chunk.NextOffset <= chunk.Offset {
		return errors.New("AgentDock Artifact chunk did not advance the stream")
	}
	if !metadataOnly && chunk.EOF != (chunk.NextOffset == chunk.Size) {
		return errors.New("AgentDock Artifact end-of-stream marker is invalid")
	}
	if metadataOnly && chunk.DataBase64 != "" {
		return errors.New("AgentDock returned payload data for a metadata-only request")
	}
	return nil
}

func (s *Server) artifactLogger() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

func setArtifactHeaders(headers http.Header, chunk agentdock.ArtifactChunk) {
	setArtifactPublicHeaders(headers)
	mimeType := strings.TrimSpace(chunk.MIMEType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	headers.Set("Content-Type", mimeType)
	headers.Set("Content-Length", strconv.FormatInt(chunk.Size, 10))
	headers.Set("Accept-Ranges", "none")
	headers.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": chunk.Filename}))
	headers.Set("Content-Security-Policy", "sandbox; default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
}

func setArtifactPublicHeaders(headers http.Header) {
	headers.Set("Cache-Control", "private, no-store")
	headers.Set("Access-Control-Allow-Origin", "*")
	headers.Set("Cross-Origin-Resource-Policy", "cross-origin")
	headers.Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) acquireArtifactDownload(nodeID string) bool {
	s.artifactDownloadsMu.Lock()
	defer s.artifactDownloadsMu.Unlock()
	if s.artifactDownloads == nil {
		s.artifactDownloads = make(map[string]int)
	}
	if s.artifactDownloads[nodeID] >= maxConcurrentArtifactDownloadsPerNode {
		return false
	}
	s.artifactDownloads[nodeID]++
	return true
}

func (s *Server) releaseArtifactDownload(nodeID string) {
	s.artifactDownloadsMu.Lock()
	defer s.artifactDownloadsMu.Unlock()
	if s.artifactDownloads[nodeID] <= 1 {
		delete(s.artifactDownloads, nodeID)
		return
	}
	s.artifactDownloads[nodeID]--
}

func (s *Server) writeArtifactBridgeError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	code := "ARTIFACT_PROXY_FAILED"
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	} else if errors.Is(err, agentdock.ErrNodeOffline) || errors.Is(err, agentdock.ErrNodeDisconnected) {
		status = http.StatusServiceUnavailable
		code = "ARTIFACT_NODE_OFFLINE"
	}
	writeError(w, status, code, err.Error())
}

func (s *Server) artifactSigningSecret() ([]byte, error) {
	s.artifactSecretMu.Lock()
	defer s.artifactSecretMu.Unlock()
	dataDir := strings.TrimSpace(s.cfg.NexusDataDir)
	if dataDir == "" {
		return nil, errors.New("NEXUS_DATA_DIR is required for Artifact URL signing")
	}
	secretPath := filepath.Join(dataDir, "secrets", "artifact-url-secret")
	secretDir := filepath.Dir(secretPath)
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		return nil, fmt.Errorf("create NexusDock secrets directory: %w", err)
	}
	if err := os.Chmod(secretDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure NexusDock secrets directory: %w", err)
	}
	if secret, err := readArtifactSigningSecret(secretPath); err == nil {
		return secret, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	secret := make([]byte, sha256.Size)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate NexusDock Artifact signing secret: %w", err)
	}
	tmp, err := os.CreateTemp(secretDir, ".artifact-url-secret-*")
	if err != nil {
		return nil, fmt.Errorf("create NexusDock Artifact signing secret temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("secure NexusDock Artifact signing secret temp file: %w", err)
	}
	if _, err := tmp.WriteString(hex.EncodeToString(secret) + "\n"); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("write NexusDock Artifact signing secret: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("sync NexusDock Artifact signing secret: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close NexusDock Artifact signing secret: %w", err)
	}
	if err := os.Link(tmpPath, secretPath); err == nil {
		syncDirectoryBestEffort(secretDir)
		return secret, nil
	} else if !os.IsExist(err) {
		return nil, fmt.Errorf("publish NexusDock Artifact signing secret: %w", err)
	}
	return readArtifactSigningSecret(secretPath)
}

func readArtifactSigningSecret(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("NexusDock Artifact signing secret must not be a symbolic link")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read NexusDock Artifact signing secret: %w", err)
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(secret) != sha256.Size {
		return nil, errors.New("NexusDock Artifact signing secret has an invalid format")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure NexusDock Artifact signing secret: %w", err)
	}
	return secret, nil
}

func syncDirectoryBestEffort(path string) {
	dir, err := os.Open(path)
	if err != nil {
		return
	}
	_ = dir.Sync()
	_ = dir.Close()
}

func signArtifactURL(secret []byte, nodeID, artifactID, filename, sha string, expires int64) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%s\x00%s\x00%s\x00%s\x00%d", nodeID, artifactID, filename, sha, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func validArtifactSHA(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
