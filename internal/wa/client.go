package wa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/wabot/internal/crypto"
	"github.com/user/wabot/internal/proto"
)

type State int

const (
	StateInit State = iota
	StateQR
	StateConnecting
	StateConnected
	StateClosed
	StateLoggedOut
)

func (s State) String() string {
	switch s {
	case StateInit:
		return "init"
	case StateQR:
		return "qr"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateClosed:
		return "closed"
	case StateLoggedOut:
		return "loggedout"
	default:
		return "unknown"
	}
}

type Message struct {
	KeyID     string
	SenderJID string
	RemoteJID string
	Text      string
	Timestamp int64
	FromMe    bool
}

type CallEvent struct {
	ID     string
	From   string
	Status string
}

type Client struct {
	authDir    string
	serverAddr string
	deviceName string

	mu           sync.Mutex
	state        State
	qrCode       string
	jid          string
	transport    *Transport
	dict         *proto.Dictionary
	pendingQuery map[string]chan *proto.Node
	closedByUser bool

	OnMessage     func(Message)
	OnCall        func([]CallEvent)
	OnStateChange func(State, string)
}

func NewClient(authDir, serverAddr, deviceName string) *Client {
	return &Client{
		authDir:      authDir,
		serverAddr:   serverAddr,
		deviceName:   deviceName,
		state:        StateInit,
		dict:         proto.NewDictionary(),
		pendingQuery: map[string]chan *proto.Node{},
	}
}

func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *Client) QR() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.qrCode
}

func (c *Client) JID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.jid
}

func (c *Client) setState(s State, reason string) {
	c.mu.Lock()
	c.state = s
	if s != StateQR {
		c.qrCode = ""
	}
	cb := c.OnStateChange
	c.mu.Unlock()
	log.Printf("[wa] состояние: %s (%s)", s, reason)
	if cb != nil {
		cb(s, reason)
	}
}

func (c *Client) Run() error {
	for {
		err := c.connectOnce()
		if err != nil {
			log.Printf("[wa] ошибка соединения: %v", err)
		}
		c.mu.Lock()
		byUser := c.closedByUser
		c.mu.Unlock()
		if byUser {
			c.setState(StateLoggedOut, "выход выполнен")
			return nil
		}
		c.setState(StateClosed, "соединение закрыто")
		log.Printf("[wa] переподключение через 3 секунды")
		time.Sleep(3 * time.Second)
	}
}

func (c *Client) connectOnce() error {
	creds, err := LoadCredentials(c.authDir)
	if err != nil {
		log.Printf("[wa] %v, начинаю новую регистрацию", err)
		creds = nil
	}
	if creds == nil {
		return c.register()
	}
	c.setState(StateConnecting, "подключение к серверу")
	transport, err := DialWithKeys(c.serverAddr, &Keys{
		StaticPriv:   creds.NoiseKey,
		StaticPub:    creds.NoiseKeyPub,
		IdentityPriv: creds.IdentityKey,
		IdentityPub:  creds.IdentityKey,
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.transport = transport
	c.jid = creds.WaID
	c.mu.Unlock()
	go c.readLoop(transport)
	c.sendInitialPresence()
	c.setState(StateConnected, fmt.Sprintf("вход как %s", creds.WaID))
	return nil
}

func (c *Client) register() error {
	c.setState(StateConnecting, "регистрация нового устройства")
	priv, pub, err := generateNoiseKey()
	if err != nil {
		return err
	}
	transport, err := DialWithKeys(c.serverAddr, &Keys{
		StaticPriv: priv,
		StaticPub:  pub,
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.transport = transport
	c.mu.Unlock()
	go c.readLoop(transport)

	ref, err := c.requestRef()
	if err != nil {
		transport.Close()
		return err
	}
	c.mu.Lock()
	c.qrCode = ref
	c.mu.Unlock()
	c.setState(StateQR, "ожидание сканирования QR")

	ticket, err := c.waitForTicket()
	if err != nil {
		transport.Close()
		return err
	}
	waID, err := c.propose(ticket)
	if err != nil {
		transport.Close()
		return err
	}
	creds := &Credentials{
		NoiseKey:    priv,
		NoiseKeyPub: pub,
		IdentityKey: pub,
		WaID:        waID,
	}
	if err := SaveCredentials(c.authDir, creds); err != nil {
		log.Printf("[wa] %v", err)
	}
	c.mu.Lock()
	c.jid = waID
	c.mu.Unlock()
	c.sendInitialPresence()
	c.setState(StateConnected, fmt.Sprintf("регистрация завершена: %s", waID))
	return nil
}

func (c *Client) requestRef() (string, error) {
	node := proto.NewNode("iq",
		proto.Attribute{Key: "id", Value: randomID()},
		proto.Attribute{Key: "type", Value: "get"},
		proto.Attribute{Key: "xmlns", Value: "w:p"},
	)
	node.AddChild(&proto.Node{Tag: "pair-device-type", Content: []*proto.Node{{Tag: c.deviceName}}})
	resp, err := c.Query(node, 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("запрос ref не выполнен: %w", err)
	}
	device := resp.GetChild("device-ref")
	if device == nil {
		return "", fmt.Errorf("в ответе нет device-ref")
	}
	attr, ok := device.FindAttr("ref")
	if !ok {
		return "", fmt.Errorf("в device-ref нет атрибута ref")
	}
	return attr.Value, nil
}

func (c *Client) waitForTicket() ([]byte, error) {
	deadline := time.After(90 * time.Second)
	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("истекло время ожидания QR")
		case frame := <-c.ticketFrames():
			return frame, nil
		}
	}
}

func (c *Client) propose(ticket []byte) (string, error) {
	iq := proto.NewNode("iq",
		proto.Attribute{Key: "id", Value: randomID()},
		proto.Attribute{Key: "to", Value: "s.whatsapp.net"},
		proto.Attribute{Key: "type", Value: "set"},
		proto.Attribute{Key: "xmlns", Value: "w:p"},
	)
	iq.AddChild(&proto.Node{Tag: "pair-device-type", Content: []*proto.Node{{Tag: c.deviceName}}})
	iq.AddChild(&proto.Node{Tag: "ref", Content: []*proto.Node{{Tag: base64Encode(ticket)}}})
	resp, err := c.Query(iq, 20*time.Second)
	if err != nil {
		return "", fmt.Errorf("propose не выполнен: %w", err)
	}
	jidNode := resp.Query("jid", "", "")
	if jidNode == nil {
		return "", fmt.Errorf("в ответе нет jid")
	}
	user, _, _, derr := c.dict.DecodeJID(jidNode.Tag)
	if derr != nil {
		return "", fmt.Errorf("битый jid: %w", derr)
	}
	return user + "@s.whatsapp.net", nil
}

func (c *Client) sendInitialPresence() {
	presence := proto.NewNode("presence", proto.Attribute{Key: "type", Value: "available"})
	frame, err := proto.Encode(presence, c.dict)
	if err != nil {
		log.Printf("[wa] не удалось закодировать presence: %v", err)
		return
	}
	c.mu.Lock()
	t := c.transport
	c.mu.Unlock()
	if t == nil {
		return
	}
	if err := t.SendFrame(frame); err != nil {
		log.Printf("[wa] не удалось отправить presence: %v", err)
	}
}

func (c *Client) SendText(to, text string) error {
	msg := proto.NewNode("message",
		proto.Attribute{Key: "id", Value: randomID()},
		proto.Attribute{Key: "to", Value: to},
		proto.Attribute{Key: "type", Value: "text"},
	)
	msg.AddChild(&proto.Node{Tag: "plain-text", Content: []*proto.Node{{Tag: text}}})
	return c.Send(msg)
}

func (c *Client) SendPTT(to, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("не удалось прочитать аудиофайл: %w", err)
	}
	mediaID := crypto.RandomBytes(32)
	randKey := crypto.RandomBytes(32)
	encKey := append([]byte{}, randKey...)
	encKey[16] ^= 0x07
	encKey[17] ^= 0x07
	encKey[18] ^= 0x07
	encKey[19] ^= 0x07
	digest := crypto.SHA512Sum(encKey)
	iv := make([]byte, 16)
	iv[0] = 9
	enc, err := crypto.EncryptAES256CTR(digest[:32], iv, raw)
	if err != nil {
		return fmt.Errorf("шифрование медиа: %w", err)
	}
	urlNode := &proto.Node{Tag: base64Encode(mediaID)}
	macAttr := proto.Attribute{Key: "mac", Value: base64Encode(randKey)}
	msg := proto.NewNode("message",
		proto.Attribute{Key: "id", Value: randomID()},
		proto.Attribute{Key: "to", Value: to},
		proto.Attribute{Key: "type", Value: "text"},
	)
	msg.AddChild(&proto.Node{
		Tag: "media-query",
		Content: []*proto.Node{{
			Tag: "media_query",
			Attrs: []proto.Attribute{
				{Key: "mime_type", Value: "audio/ogg; codecs=opus"},
				{Key: "direct_path", Value: "default"},
			},
		}},
	})
	resp, err := c.Query(msg, 15*time.Second)
	if err != nil {
		return fmt.Errorf("media-query: %w", err)
	}
	directPath := "default"
	if mq := resp.GetChild("media-query-response"); mq != nil {
		if inner := mq.GetChild("media_query_response"); inner != nil {
			if v, ok := inner.FindAttr("direct_path"); ok && v.Value != "" {
				directPath = v.Value
			}
		}
	}
	iq := proto.NewNode("iq",
		proto.Attribute{Key: "id", Value: randomID()},
		proto.Attribute{Key: "to", Value: "s.whatsapp.net"},
		proto.Attribute{Key: "type", Value: "set"},
	)
	upload := proto.NewNode("upload",
		proto.Attribute{Key: "realm", Value: "row"},
		proto.Attribute{Key: "resumable", Value: "true"},
		proto.Attribute{Key: "mime_type", Value: "audio/ogg; codecs=opus"},
		proto.Attribute{Key: "media_type", Value: "ptt"},
	)
	upload.Content = append(upload.Content, urlNode)
	upload.AddChild(proto.NewNode("enc",
		proto.Attribute{Key: "key", Value: base64Encode(randKey)},
		macAttr,
	))
	iq.AddChild(upload)
	respIQ, err := c.Query(iq, 20*time.Second)
	if err != nil {
		return fmt.Errorf("запрос URL загрузки: %w", err)
	}
	success := respIQ.GetChild("upload-url")
	if success == nil {
		return fmt.Errorf("сервер не вернул upload-url")
	}
	hostURL, _ := success.FindAttr("host")
	mediaConn, _ := success.FindAttr("media_conn")
	req, err := http.NewRequest(http.MethodPost, hostURL.Value+"/"+mediaConn.Value, bytes.NewReader(enc))
	if err != nil {
		return fmt.Errorf("подготовка запроса загрузки: %w", err)
	}
	req.Header.Set("Origin", "https://web.whatsapp.com")
	req.ContentLength = int64(len(enc))
	client := &http.Client{Timeout: 60 * time.Second}
	httpResp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("загрузка медиа: %w", err)
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("загрузка медиа: код %d: %s", httpResp.StatusCode, string(body))
	}
	var respObj struct {
		MediaKey       string `json:"media_key"`
		FileHashSHA256 string `json:"file_hash_sha256"`
		MediaHandle    string `json:"media_handle"`
	}
	if err := json.Unmarshal(body, &respObj); err != nil {
		return fmt.Errorf("разбор ответа загрузки: %w", err)
	}
	msgNode := proto.NewNode("message",
		proto.Attribute{Key: "id", Value: randomID()},
		proto.Attribute{Key: "to", Value: to},
		proto.Attribute{Key: "type", Value: "text"},
	)
	msgNode.AddChild(&proto.Node{
		Tag: "audio",
		Attrs: []proto.Attribute{
			{Key: "direct_path", Value: directPath},
			{Key: "ptt", Value: "1"},
			{Key: "media_key", Value: respObj.MediaKey},
			{Key: "enc", Value: "aes-ctr"},
			{Key: "mimetype", Value: "audio/ogg; codecs=opus"},
			{Key: "file_hash", Value: respObj.FileHashSHA256},
			{Key: "media_hdr", Value: base64Encode(mediaID)},
		},
		Content: []*proto.Node{{Tag: urlNode.Tag}},
	})
	if err := c.Send(msgNode); err != nil {
		return fmt.Errorf("отправка PTT-сообщения: %w", err)
	}
	log.Printf("[wa] PTT отправлено в %s (%s)", to, filepath.Base(path))
	return nil
}

func (c *Client) Send(node *proto.Node) error {
	frame, err := proto.Encode(node, c.dict)
	if err != nil {
		return fmt.Errorf("кодирование stanza: %w", err)
	}
	c.mu.Lock()
	t := c.transport
	c.mu.Unlock()
	if t == nil {
		return fmt.Errorf("нет активного соединения")
	}
	return t.SendFrame(frame)
}

func (c *Client) Query(node *proto.Node, timeout time.Duration) (*proto.Node, error) {
	id, ok := node.FindAttr("id")
	if !ok {
		return nil, fmt.Errorf("у iq нет id")
	}
	ch := make(chan *proto.Node, 1)
	c.mu.Lock()
	c.pendingQuery[id.Value] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pendingQuery, id.Value)
		c.mu.Unlock()
	}()
	if err := c.Send(node); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("таймаут запроса %s", id.Value)
	}
}

func (c *Client) readLoop(t *Transport) {
	for {
		frame, err := t.Receive()
		if err != nil {
			log.Printf("[wa] приём прерван: %v", err)
			c.mu.Lock()
			c.transport = nil
			c.mu.Unlock()
			t.Close()
			c.triggerReconnect()
			return
		}
		c.handleFrame(frame)
	}
}

func (c *Client) triggerReconnect() {
	c.mu.Lock()
	if !c.closedByUser {
		c.mu.Unlock()
		c.Close()
		return
	}
	c.mu.Unlock()
}

func (c *Client) handleFrame(frame []byte) {
	node, err := c.decodeFrame(frame)
	if err != nil {
		log.Printf("[wa] не удалось разобрать кадр: %v", err)
		return
	}
	if node == nil {
		return
	}
	c.dispatch(node)
}

func (c *Client) decodeFrame(frame []byte) (*proto.Node, error) {
	decoder := proto.NewDecoder(frame, c.dict)
	return proto.ParseTree(decoder)
}

func (c *Client) dispatch(node *proto.Node) {
	switch node.Tag {
	case "iq":
		c.dispatchIQ(node)
	case "message":
		c.dispatchMessage(node)
	case "call":
		c.dispatchCall(node)
	default:
		log.Printf("[wa] получен узел %s", node.Tag)
	}
}

func (c *Client) dispatchIQ(node *proto.Node) {
	id, ok := node.FindAttr("id")
	if !ok {
		return
	}
	c.mu.Lock()
	ch := c.pendingQuery[id.Value]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- node:
		default:
			log.Printf("[wa] канал ответа %s переполнен", id.Value)
		}
		return
	}
	if node.GetChild("pair-device-type") != nil || node.Query("device", "", "") != nil {
		c.pushTicket(node)
	}
}

func (c *Client) dispatchMessage(node *proto.Node) {
	from, _ := node.FindAttr("from")
	id, _ := node.FindAttr("id")
	ts, _ := node.FindAttr("t")
	text := extractText(node)
	msg := Message{
		KeyID:     id.Value,
		SenderJID: from.Value,
		RemoteJID: from.Value,
		Text:      text,
		Timestamp: parseIntDefault(ts.Value),
	}
	c.mu.Lock()
	cb := c.OnMessage
	c.mu.Unlock()
	if cb != nil {
		cb(msg)
	}
}

func (c *Client) dispatchCall(node *proto.Node) {
	var events []CallEvent
	for _, child := range node.Content {
		callID, _ := child.FindAttr("call-id")
		from, _ := node.FindAttr("from")
		events = append(events, CallEvent{
			ID:     callID.Value,
			From:   from.Value,
			Status: child.Tag,
		})
	}
	if len(events) == 0 {
		return
	}
	c.mu.Lock()
	cb := c.OnCall
	c.mu.Unlock()
	if cb != nil {
		cb(events)
	}
}

func extractText(node *proto.Node) string {
	plain := node.GetChild("plain-text")
	if plain != nil && len(plain.Content) > 0 {
		return plain.Content[0].Tag
	}
	body := node.GetChild("body")
	if body != nil && len(body.Content) > 0 {
		return body.Content[0].Tag
	}
	return ""
}

var ticketCh chan []byte

func (c *Client) ticketFrames() chan []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ticketCh == nil {
		ticketCh = make(chan []byte, 8)
	}
	return ticketCh
}

func (c *Client) pushTicket(node *proto.Node) {
	c.mu.Lock()
	ch := ticketCh
	c.mu.Unlock()
	if ch == nil {
		return
	}
	data := node.GetDataBytes()
	if data == nil {
		return
	}
	select {
	case ch <- data:
	default:
		log.Printf("[wa] очередь тикетов переполнена")
	}
}

func (c *Client) Logout() error {
	c.mu.Lock()
	c.closedByUser = true
	t := c.transport
	c.mu.Unlock()
	if t != nil {
		return t.Close()
	}
	return nil
}

func (c *Client) Close() {
	c.mu.Lock()
	t := c.transport
	c.mu.Unlock()
	if t != nil {
		t.Close()
	}
}
