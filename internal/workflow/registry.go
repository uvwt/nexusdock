package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// OperationError 把注册表的业务失败带上稳定错误码。
// HTTP/MCP 边界负责把业务错误码映射成各自协议语义，workflow 包不依赖传输层。
type OperationError struct {
	Code string
	err  error
}

func (e *OperationError) Error() string { return e.err.Error() }
func (e *OperationError) Unwrap() error { return e.err }

func operationError(code string, err error) error {
	return &OperationError{Code: code, err: err}
}

// Registry 是 NexusDock 自有 Workflow 模板注册表：published 目录下的
// 模板文件是唯一事实来源，所有读写都经过这里的加锁方法串行执行。
type Registry struct {
	// mu 串行化全部注册表操作：published 文件没有真正的并发编辑场景，
	// 互斥只是为了避免发布/退役/列表交错导致读到半更新的目录状态。
	mu   sync.Mutex
	root string
}

type publishIntent struct {
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
}

// NewRegistry 使用组合根传入的数据目录；published 文件与向量索引都存放在其下。
func NewRegistry(root string) *Registry {
	return &Registry{root: root}
}

// Root 返回注册表根目录的绝对路径，HTTP/MCP 响应用它向用户暴露存储位置。
func (r *Registry) Root() string { return r.root }

// PublishedFilePath 返回已发布模板文件的绝对路径。文件大小、修改时间这类
// 元数据属于注册表的存储布局，HTTP/MCP 组装 summary 时需要用它定位真实文件。
func (r *Registry) PublishedFilePath(id, version string) string {
	return r.templatePath("published", id, version)
}

func (r *Registry) templatePath(area, id, version string) string {
	return filepath.Join(r.root, area, id+"@"+version+".json")
}

func (r *Registry) ensureDirs() error {
	dir := filepath.Join(r.root, "published")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// 显式收紧权限：NexusDataDir 可能位于被共享的外置卷上，
	// umask 之外再 chmod 一次，保证模板文件目录不被其他用户读取。
	return os.Chmod(dir, 0o700)
}

// Publish 发布一个新版本：这是唯一的写入口。调用方携带的生命周期元数据
// （status/hash/时间戳）一律忽略，避免伪造状态或复用旧哈希；同版本已发布
// 则拒绝覆盖。发布前先落盘 intent，保证跨多个模板文件的状态在崩溃后可幂等前滚。
func (r *Registry) Publish(input Template) (Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t := input
	t.Status = StatusActive
	t.Hash = ""
	t.PublishedAt = nil
	t.RetiredAt = nil
	if err := r.ensureDirs(); err != nil {
		return Template{}, operationError("WORKFLOW_REGISTRY_FAILED", err)
	}
	if err := r.recoverPendingPublishLocked(); err != nil {
		return Template{}, operationError("WORKFLOW_REGISTRY_FAILED", err)
	}
	if err := validateTemplate(t); err != nil {
		return Template{}, operationError("INVALID_WORKFLOW_TEMPLATE", err)
	}
	if _, err := os.Stat(r.templatePath("published", t.ID, t.Version)); err == nil {
		return Template{}, operationError("WORKFLOW_VERSION_IMMUTABLE", errors.New("published template version already exists and cannot be overwritten"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return Template{}, operationError("WORKFLOW_REGISTRY_FAILED", err)
	}

	now := time.Now().UTC()
	t.PublishedAt = &now
	t.Hash = templateHash(t)
	intent := publishIntent{ID: t.ID, Version: t.Version, PublishedAt: now}
	if err := writeTemplateJSON(r.publishIntentPath(), intent); err != nil {
		return Template{}, operationError("WORKFLOW_PUBLISH_FAILED", fmt.Errorf("persist publish intent: %w", err))
	}
	errorCode, err := r.publishWithWriter(t, now, writeTemplateJSON)
	if err != nil {
		// intent 必须保留：目标文件可能已经原子落盘，也可能尚未出现。
		// 下一次访问由 recoverPendingPublishLocked 根据真实文件状态幂等前滚或清理。
		return Template{}, operationError(errorCode, err)
	}
	if err := r.clearPublishIntent(); err != nil {
		return Template{}, operationError("WORKFLOW_REGISTRY_FAILED", fmt.Errorf("clear publish intent: %w", err))
	}
	return t, nil
}

// Retire 显式退役一个 active 版本；已退役版本再次退役会被拒绝。
func (r *Registry) Retire(id, version string) (Template, error) {
	if !ValidToken(id) || !ValidToken(version) {
		return Template{}, operationError("INVALID_WORKFLOW_TEMPLATE", errors.New("template id or version is invalid"))
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureDirs(); err != nil {
		return Template{}, operationError("WORKFLOW_REGISTRY_FAILED", err)
	}
	if err := r.recoverPendingPublishLocked(); err != nil {
		return Template{}, operationError("WORKFLOW_REGISTRY_FAILED", err)
	}
	t, err := r.load("published", id, version)
	if err != nil {
		return Template{}, operationError("WORKFLOW_TEMPLATE_NOT_FOUND", err)
	}
	if t.Status != StatusActive {
		return Template{}, operationError("WORKFLOW_TEMPLATE_NOT_ACTIVE", errors.New("only active templates can be retired"))
	}

	now := time.Now().UTC()
	t.Status = StatusRetired
	t.RetiredAt = &now
	t.Hash = templateHash(t)
	if err := writeTemplateJSON(r.templatePath("published", id, version), t); err != nil {
		return Template{}, operationError("WORKFLOW_RETIRE_FAILED", err)
	}
	return t, nil
}

// Get 读取一个具体发布版本；文件损坏必须显式报错，不能当成“不存在”。
func (r *Registry) Get(id, version string) (Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureDirs(); err != nil {
		return Template{}, err
	}
	if err := r.recoverPendingPublishLocked(); err != nil {
		return Template{}, err
	}
	t, err := r.load("published", id, version)
	if err == nil {
		return t, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Template{}, fmt.Errorf("template %s@%s not found", id, version)
	}
	return Template{}, fmt.Errorf("load published template %s@%s: %w", id, version, err)
}

// Active 返回指定 ID 当前 active 的版本；MCP 的 get（不带版本）与 get_many 依赖它。
func (r *Registry) Active(id string) (Template, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Template{}, errors.New("template_id is required")
	}
	templates, err := r.List(StatusActive)
	if err != nil {
		return Template{}, err
	}
	for _, template := range templates {
		if template.ID == id {
			return template, nil
		}
	}
	return Template{}, fmt.Errorf("active workflow template %s not found", id)
}

// List 按状态过滤 published 目录中的模板（status 为空返回全部），
// 结果按 ID 升序、同 ID 按版本降序排列，保证响应顺序稳定。
func (r *Registry) List(status Status) ([]Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listLocked(status)
}

func (r *Registry) listLocked(status Status) ([]Template, error) {
	if err := r.ensureDirs(); err != nil {
		return nil, err
	}
	if err := r.recoverPendingPublishLocked(); err != nil {
		return nil, err
	}

	dir := filepath.Join(r.root, "published")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Template, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		t, err := decodeTemplate(data)
		if err != nil {
			return nil, fmt.Errorf("read workflow template %s: %w", entry.Name(), err)
		}
		if status == "" || t.Status == status {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return CompareVersions(out[i].Version, out[j].Version) > 0
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *Registry) load(area, id, version string) (Template, error) {
	if !ValidToken(id) || !ValidToken(version) {
		return Template{}, errors.New("invalid template id or version")
	}
	data, err := os.ReadFile(r.templatePath(area, id, version))
	if err != nil {
		return Template{}, err
	}
	return decodeTemplate(data)
}

// decodeTemplate 使用严格 JSON：未知字段与多余 JSON 值都视为模板文件损坏。
func decodeTemplate(data []byte) (Template, error) {
	var template Template
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&template); err != nil {
		return Template{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return Template{}, errors.New("template file contains multiple JSON values")
		}
		return Template{}, fmt.Errorf("read trailing template data: %w", err)
	}
	return template, nil
}

func (r *Registry) publishIntentPath() string {
	return filepath.Join(r.root, ".publish-intent.json")
}

func (r *Registry) clearPublishIntent() error {
	return removeFile(r.publishIntentPath())
}

// recoverPendingPublishLocked 收敛进程在发布中途退出留下的多 active 状态。
// intent 在新版本落盘前先持久化，因此恢复时可以明确知道哪一个版本是目标版本，
// 不依赖版本号大小或墙上时钟猜测。调用方必须持有 r.mu。
func (r *Registry) recoverPendingPublishLocked() error {
	data, err := os.ReadFile(r.publishIntentPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read workflow publish intent: %w", err)
	}
	var intent publishIntent
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil {
		return fmt.Errorf("decode workflow publish intent: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("workflow publish intent contains multiple JSON values")
		}
		return fmt.Errorf("read trailing workflow publish intent data: %w", err)
	}
	if !ValidToken(intent.ID) || !ValidToken(intent.Version) || intent.PublishedAt.IsZero() {
		return errors.New("workflow publish intent is invalid")
	}
	target, err := r.load("published", intent.ID, intent.Version)
	if errors.Is(err, os.ErrNotExist) {
		// 进程在 intent 持久化后、目标版本真正落盘前退出；旧 active 从未被修改。
		return r.clearPublishIntent()
	}
	if err != nil {
		return fmt.Errorf("load workflow publish target during recovery: %w", err)
	}
	if target.Status != StatusActive {
		return fmt.Errorf("workflow publish target %s@%s is not active during recovery", intent.ID, intent.Version)
	}
	if err := r.retireActiveWithWriter(intent.ID, intent.Version, intent.PublishedAt, writeTemplateJSON); err != nil {
		return fmt.Errorf("recover workflow publish: %w", err)
	}
	return r.clearPublishIntent()
}

// jsonWriter 是发布/退役落盘的写入函数，测试用它注入磁盘故障来验证回滚行为。
type jsonWriter func(string, any) error

// publishWithWriter 先写新版本文件，再退役同 ID 的旧 active 版本。
// 新版本一旦可能落盘就不再删除：生产路径已经先持久化 publish intent，
// 任一后续失败都由下一次访问按 intent 幂等前滚，避免“回滚本身失败”再次制造半提交。
func (r *Registry) publishWithWriter(t Template, publishedAt time.Time, write jsonWriter) (string, error) {
	publishedPath := r.templatePath("published", t.ID, t.Version)
	if err := write(publishedPath, t); err != nil {
		return "WORKFLOW_PUBLISH_FAILED", fmt.Errorf("write new published template: %w", err)
	}
	if err := r.retireActiveWithWriter(t.ID, t.Version, publishedAt, write); err != nil {
		return "WORKFLOW_RETIRE_OLD_FAILED", fmt.Errorf("retire old templates: %w", err)
	}
	return "", nil
}

// retireActiveWithWriter 把同 ID 的其它 active 版本标记为退役。逐个写入时
// 记录原始内容，一旦中途失败就按顺序回写，尽力保证目录回到发布前状态。
func (r *Registry) retireActiveWithWriter(id, exceptVersion string, retiredAt time.Time, write jsonWriter) error {
	entries, err := os.ReadDir(filepath.Join(r.root, "published"))
	if err != nil {
		return err
	}
	originals := make([]Template, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.root, "published", entry.Name()))
		if err != nil {
			return err
		}
		t, err := decodeTemplate(data)
		if err != nil {
			return fmt.Errorf("read workflow template %s: %w", entry.Name(), err)
		}
		if t.ID != id || t.Version == exceptVersion || t.Status != StatusActive {
			continue
		}
		originals = append(originals, t)
	}

	updated := make([]Template, 0, len(originals))
	for _, original := range originals {
		retired := original
		retired.Status = StatusRetired
		retired.RetiredAt = &retiredAt
		retired.Hash = templateHash(retired)
		if err := write(r.templatePath("published", retired.ID, retired.Version), retired); err != nil {
			rollbackErrors := make([]string, 0)
			rollbackTargets := append(append([]Template{}, updated...), original)
			for _, previous := range rollbackTargets {
				if rollbackErr := write(r.templatePath("published", previous.ID, previous.Version), previous); rollbackErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s@%s: %v", previous.ID, previous.Version, rollbackErr))
				}
			}
			if len(rollbackErrors) > 0 {
				return fmt.Errorf("retire %s@%s: %w; rollback failures: %s", retired.ID, retired.Version, err, strings.Join(rollbackErrors, "; "))
			}
			return fmt.Errorf("retire %s@%s: %w", retired.ID, retired.Version, err)
		}
		updated = append(updated, original)
	}
	return nil
}

// writeTemplateJSON 用“临时文件 + rename + 目录 fsync”的原子写法落盘，
// 进程崩溃或断电都不会留下半截模板文件被后续读取。
func writeTemplateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(path)
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(path)
}

func syncDir(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
