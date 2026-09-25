package agentdock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/nexusdock/internal/core"
)

const (
	maxConnectionMessageBytes = 8 << 20
	heartbeatInterval         = 30 * time.Second
)

var (
	ErrNodeOffline      = errors.New("AgentDock 节点当前离线")
	ErrNodeDisconnected = errors.New("AgentDock 节点连接已断开")
)

type pendingResult struct {
	result json.RawMessage
	err    error
}

type nodeConnection struct {
	nodeID    string
	socket    *websocket.Conn
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan pendingResult
	closed    bool
	ready     chan struct{}
	readyOnce sync.Once
}

type Hub struct {
	store   *Store
	mu      sync.RWMutex
	nodes   map[string]*nodeConnection
	onHello func(Node, Hello)
}

func (h *Hub) SetHelloHandler(handler func(Node, Hello)) {
	h.mu.Lock()
	h.onHello = handler
	h.mu.Unlock()
}

func NewHub(store *Store) *Hub {
	return &Hub{store: store, nodes: make(map[string]*nodeConnection)}
}

func (h *Hub) Online(nodeID string) bool {
	h.mu.RLock()
	connection := h.nodes[nodeID]
	h.mu.RUnlock()
	return connection != nil && connection.isReady()
}

func (h *Hub) Disconnect(nodeID string) {
	h.mu.Lock()
	connection := h.nodes[nodeID]
	delete(h.nodes, nodeID)
	h.mu.Unlock()
	if connection != nil {
		connection.close(ErrNodeDisconnected)
	}
}

func (h *Hub) Accept(w http.ResponseWriter, r *http.Request, nodeID, publicURL string) error {
	node, err := h.store.Get(r.Context(), nodeID)
	if err != nil {
		return err
	}
	if !node.Enabled {
		return ErrNodeDisabled
	}

	upgrader := websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		CheckOrigin: func(request *http.Request) bool {
			// 节点连接不是浏览器会话，不使用 Origin 作为身份依据；身份由 Device Token 固定绑定。
			return request.Header.Get("Origin") == ""
		},
	}
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	socket.SetReadLimit(maxConnectionMessageBytes)
	_ = socket.SetReadDeadline(time.Now().Add(15 * time.Second))
	connection := &nodeConnection{
		nodeID: nodeID, socket: socket, pending: make(map[string]chan pendingResult), ready: make(chan struct{}),
	}

	var first connectionMessage
	if err := socket.ReadJSON(&first); err != nil {
		connection.close(err)
		return fmt.Errorf("读取 AgentDock 握手: %w", err)
	}
	if first.Type != protocol.MessageNodeHello || first.Hello == nil || first.ProtocolVersion != ConnectionProtocolVersion || first.Hello.ProtocolVersion != ConnectionProtocolVersion {
		connection.close(errors.New("invalid AgentDock handshake"))
		return errors.New("AgentDock 节点握手无效")
	}
	updated, err := h.store.UpdateHello(r.Context(), nodeID, *first.Hello)
	if err != nil {
		connection.close(err)
		return err
	}
	// 先登记连接，再发送 node.ready；Invoke 会等待 connection.ready，保证客户端一旦收到
	// ready 后立即发起 Runtime 请求时，不会撞上“ready 已到达但 Hub 尚未登记”的竞态。
	h.mu.Lock()
	previous := h.nodes[nodeID]
	h.nodes[nodeID] = connection
	onHello := h.onHello
	h.mu.Unlock()
	if err := connection.write(connectionMessage{
		Type: protocol.MessageNodeReady, ProtocolVersion: ConnectionProtocolVersion, HeartbeatMS: int(heartbeatInterval / time.Millisecond),
		PublicURL: strings.TrimRight(strings.TrimSpace(publicURL), "/"),
	}); err != nil {
		h.mu.Lock()
		if h.nodes[nodeID] == connection {
			if previous != nil {
				h.nodes[nodeID] = previous
			} else {
				delete(h.nodes, nodeID)
			}
		}
		h.mu.Unlock()
		connection.close(err)
		return fmt.Errorf("确认 AgentDock 握手: %w", err)
	}
	connection.markReady()
	_ = socket.SetReadDeadline(time.Now().Add(2 * heartbeatInterval))

	if previous != nil {
		previous.close(errors.New("AgentDock 节点建立了新连接"))
	}
	if onHello != nil {
		onHello(updated, *first.Hello)
	}

	go h.readLoop(connection)
	return nil
}

func (h *Hub) Invoke(ctx context.Context, nodeID, operation string, arguments any) (map[string]any, error) {
	h.mu.RLock()
	connection := h.nodes[nodeID]
	h.mu.RUnlock()
	if connection == nil {
		return nil, ErrNodeOffline
	}
	if operation == "" {
		return nil, errors.New("节点操作不能为空")
	}
	if err := connection.waitReady(ctx); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("编码节点调用参数: %w", err)
	}
	requestID, err := core.NewID("req")
	if err != nil {
		return nil, err
	}
	resultChannel := make(chan pendingResult, 1)
	if err := connection.addPending(requestID, resultChannel); err != nil {
		return nil, err
	}
	defer connection.removePending(requestID)

	if err := connection.write(connectionMessage{Type: protocol.MessageToolInvoke, RequestID: requestID, Operation: operation, Arguments: encoded}); err != nil {
		connection.close(err)
		return nil, ErrNodeDisconnected
	}
	select {
	case <-ctx.Done():
		_ = connection.write(connectionMessage{Type: protocol.MessageToolCancel, RequestID: requestID})
		return nil, ctx.Err()
	case response := <-resultChannel:
		if response.err != nil {
			return nil, response.err
		}
		var result map[string]any
		if err := json.Unmarshal(response.result, &result); err != nil {
			return nil, fmt.Errorf("解析 AgentDock 节点结果: %w", err)
		}
		return result, nil
	}
}

func (h *Hub) readLoop(connection *nodeConnection) {
	defer func() {
		h.mu.Lock()
		if h.nodes[connection.nodeID] == connection {
			delete(h.nodes, connection.nodeID)
		}
		h.mu.Unlock()
		connection.close(ErrNodeDisconnected)
	}()
	for {
		var message connectionMessage
		if err := connection.socket.ReadJSON(&message); err != nil {
			return
		}
		_ = connection.socket.SetReadDeadline(time.Now().Add(2 * heartbeatInterval))
		switch message.Type {
		case protocol.MessageToolResult:
			connection.resolve(message.RequestID, pendingResult{result: message.Result})
		case protocol.MessageToolError:
			if message.Error == nil {
				message.Error = &RemoteError{Code: "NODE_BAD_RESPONSE", Message: "AgentDock 返回了空错误"}
			}
			connection.resolve(message.RequestID, pendingResult{err: message.Error})
		case protocol.MessageNodeHeartbeat:
			_ = h.store.Touch(context.Background(), connection.nodeID)
			_ = connection.write(connectionMessage{Type: protocol.MessageNodeHeartbeat})
		}
	}
}

func (c *nodeConnection) markReady() {
	c.readyOnce.Do(func() { close(c.ready) })
}

func (c *nodeConnection) isReady() bool {
	select {
	case <-c.ready:
		return true
	default:
		return false
	}
}

func (c *nodeConnection) waitReady(ctx context.Context) error {
	select {
	case <-c.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *nodeConnection) write(message connectionMessage) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrNodeDisconnected
	}
	_ = c.socket.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return c.socket.WriteJSON(message)
}

func (c *nodeConnection) addPending(requestID string, result chan pendingResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrNodeDisconnected
	}
	c.pending[requestID] = result
	return nil
}

func (c *nodeConnection) removePending(requestID string) {
	c.mu.Lock()
	delete(c.pending, requestID)
	c.mu.Unlock()
}

func (c *nodeConnection) resolve(requestID string, result pendingResult) {
	c.mu.Lock()
	channel := c.pending[requestID]
	delete(c.pending, requestID)
	c.mu.Unlock()
	if channel != nil {
		channel <- result
	}
}

func (c *nodeConnection) close(reason error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = make(map[string]chan pendingResult)
	c.mu.Unlock()
	c.markReady()
	_ = c.socket.Close()
	for _, channel := range pending {
		channel <- pendingResult{err: reason}
	}
}
