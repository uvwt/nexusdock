package httpx

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/agentdock"
)

const maxProxiedArtifactBytes = 512 << 20

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
	if r.Header.Get("Range") != "" {
		w.Header().Set("Content-Range", "bytes */*")
		http.Error(w, http.StatusText(http.StatusRequestedRangeNotSatisfiable), http.StatusRequestedRangeNotSatisfiable)
		return
	}

	if r.Method == http.MethodHead {
		chunk, readErr := s.agentDockHub.ReadArtifactChunk(r.Context(), nodeID, artifactID, 0, 0)
		if readErr != nil {
			s.writeArtifactBridgeError(w, readErr)
			return
		}
		if err := validateArtifactChunk(chunk, artifactID, filename, sha, expires, 0, true); err != nil {
			writeError(w, http.StatusBadGateway, "ARTIFACT_NODE_RESPONSE_INVALID", err.Error())
			return
		}
		setArtifactHeaders(w.Header(), chunk)
		w.WriteHeader(http.StatusOK)
		return
	}

	first, err := s.agentDockHub.ReadArtifactChunk(r.Context(), nodeID, artifactID, 0, agentdock.MaxArtifactChunkBytes)
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
	setArtifactHeaders(w.Header(), first)
	w.WriteHeader(http.StatusOK)

	hasher := sha256.New()
	offset := int64(0)
	chunk := first
	for {
		data, decodeErr := base64.StdEncoding.DecodeString(chunk.DataBase64)
		if decodeErr != nil || chunk.NextOffset != offset+int64(len(data)) || len(data) > agentdock.MaxArtifactChunkBytes {
			s.artifactLogger().Error("invalid AgentDock Artifact chunk", "node_id", nodeID, "artifact_id", artifactID, "offset", offset)
			return
		}
		if len(data) > 0 {
			if _, err := io.MultiWriter(w, hasher).Write(data); err != nil {
				return
			}
		}
		offset = chunk.NextOffset
		if chunk.EOF {
			break
		}
		chunk, err = s.agentDockHub.ReadArtifactChunk(r.Context(), nodeID, artifactID, offset, agentdock.MaxArtifactChunkBytes)
		if err != nil {
			s.artifactLogger().Error("read AgentDock Artifact chunk", "node_id", nodeID, "artifact_id", artifactID, "offset", offset, "error", err)
			return
		}
		if err := validateArtifactChunk(chunk, artifactID, filename, sha, expires, offset, false); err != nil {
			s.artifactLogger().Error("validate AgentDock Artifact chunk", "node_id", nodeID, "artifact_id", artifactID, "offset", offset, "error", err)
			return
		}
	}
	if offset != first.Size || hex.EncodeToString(hasher.Sum(nil)) != sha {
		s.artifactLogger().Error("AgentDock Artifact stream checksum mismatch", "node_id", nodeID, "artifact_id", artifactID, "bytes", offset)
	}
}

func validateArtifactChunk(chunk agentdock.ArtifactChunk, artifactID, filename, sha string, expires, offset int64, metadataOnly bool) error {
	if chunk.ArtifactID != artifactID || chunk.Filename != filename || strings.ToLower(chunk.SHA256) != sha || chunk.ExpiresAt.Unix() != expires {
		return errors.New("AgentDock Artifact metadata does not match the signed URL")
	}
	if chunk.Size < 0 || chunk.Size > maxProxiedArtifactBytes || chunk.Offset != offset {
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
	mimeType := strings.TrimSpace(chunk.MIMEType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	headers.Set("Content-Type", mimeType)
	headers.Set("Content-Length", strconv.FormatInt(chunk.Size, 10))
	headers.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": chunk.Filename}))
	headers.Set("Cache-Control", "private, no-store")
	headers.Set("Access-Control-Allow-Origin", "*")
	headers.Set("Cross-Origin-Resource-Policy", "cross-origin")
	headers.Set("Content-Security-Policy", "sandbox; default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	headers.Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) writeArtifactBridgeError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	code := "ARTIFACT_PROXY_FAILED"
	if errors.Is(err, agentdock.ErrNodeOffline) || errors.Is(err, agentdock.ErrNodeDisconnected) {
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
	if secret, err := os.ReadFile(secretPath); err == nil {
		if len(secret) != sha256.Size {
			return nil, errors.New("NexusDock Artifact signing secret has an invalid size")
		}
		return secret, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read NexusDock Artifact signing secret: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		return nil, fmt.Errorf("create NexusDock secrets directory: %w", err)
	}
	secret := make([]byte, sha256.Size)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate NexusDock Artifact signing secret: %w", err)
	}
	file, err := os.OpenFile(secretPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			stored, readErr := os.ReadFile(secretPath)
			if readErr == nil && len(stored) == sha256.Size {
				return stored, nil
			}
		}
		return nil, fmt.Errorf("create NexusDock Artifact signing secret: %w", err)
	}
	if _, err := file.Write(secret); err != nil {
		_ = file.Close()
		_ = os.Remove(secretPath)
		return nil, fmt.Errorf("write NexusDock Artifact signing secret: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(secretPath)
		return nil, fmt.Errorf("close NexusDock Artifact signing secret: %w", err)
	}
	return secret, nil
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
