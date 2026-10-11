package calls

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/user/wabot/internal/config"
	"github.com/user/wabot/internal/player"
	"github.com/user/wabot/internal/proto"
	"github.com/user/wabot/internal/wa"
)

type State string

const (
	StateIdle    State = "idle"
	StateDialing State = "dialing"
	StateRinging State = "ringing"
	StateActive  State = "active"
)

type Status struct {
	State   string `json:"state"`
	JID     string `json:"jid,omitempty"`
	Playing bool   `json:"playing"`
	Loop    bool   `json:"loop"`
	Muted   bool   `json:"muted"`
}

type Manager struct {
	mu       sync.Mutex
	sock     *wa.Client
	cfg      config.Voip
	state    State
	jid      string
	callID   string
	loop     bool
	muted    bool
	pending  string
	mediaPath string
	player   *player.Player
	onState  func(string)
	acceptAt time.Time
}

func NewManager(sock *wa.Client, cfg config.Voip) *Manager {
	return &Manager{sock: sock, cfg: cfg, state: StateIdle}
}

func (m *Manager) SetOnState(fn func(string)) {
	m.mu.Lock()
	m.onState = fn
	m.mu.Unlock()
}

func (m *Manager) Loop() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loop
}

func (m *Manager) ToggleLoop() bool {
	m.mu.Lock()
	m.loop = !m.loop
	v := m.loop
	m.mu.Unlock()
	return v
}

func (m *Manager) Muted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.muted
}

func (m *Manager) SetMuted(v bool) {
	m.mu.Lock()
	m.muted = v
	inCall := m.state != StateIdle
	m.mu.Unlock()
	if inCall {
		m.sendProps()
	}
}

func (m *Manager) Dial(jid string) error {
	m.mu.Lock()
	if m.state != StateIdle {
		m.mu.Unlock()
		return fmt.Errorf("звонок уже активен")
	}
	m.jid = jid
	m.callID = randomCallID()
	m.state = StateDialing
	m.acceptAt = time.Now().Add(60 * time.Second)
	m.mu.Unlock()

	offer := m.buildOffer(jid)
	if err := m.sock.Send(offer); err != nil {
		m.mu.Lock()
		m.state = StateIdle
		m.jid = ""
		m.callID = ""
		m.mu.Unlock()
		return fmt.Errorf("не удалось отправить offer: %w", err)
	}
	go m.watchTimeout()
	return nil
}

func (m *Manager) watchTimeout() {
	for {
		time.Sleep(time.Second)
		m.mu.Lock()
		expired := m.state == StateDialing && time.Now().After(m.acceptAt)
		m.mu.Unlock()
		if expired {
			log.Printf("[call] нет ответа на звонок, сброс состояния")
			m.Hangup()
			return
		}
		m.mu.Lock()
		done := m.state == StateIdle
		m.mu.Unlock()
		if done {
			return
		}
	}
}

func (m *Manager) buildOffer(jid string) *proto.Node {
	m.mu.Lock()
	callID := m.callID
	m.mu.Unlock()
	sdp := sdpPayload(callID)
	iq := proto.NewNode("iq",
		proto.Attribute{Key: "id", Value: callID},
		proto.Attribute{Key: "type", Value: "set"},
		proto.Attribute{Key: "xmlns", Value: "call"},
		proto.Attribute{Key: "to", Value: jid},
	)
	call := proto.NewNode("call",
		proto.Attribute{Key: "call-id", Value: callID},
		proto.Attribute{Key: "call-creator", Value: creatorTag(callID)},
	)
	offer := proto.NewNode("offer",
		proto.Attribute{Key: "call-id", Value: callID},
		proto.Attribute{Key: "from-prekey", Value: "-1"},
		proto.Attribute{Key: "cc_ver", Value: "5"},
		proto.Attribute{Key: "lt", Value: "md"},
		proto.Attribute{Key: "media", Value: "audio"},
		proto.Attribute{Key: "rtx_allowed", Value: "true"},
	)
	data := &proto.Node{
		Attrs: []proto.Attribute{{Key: "encoding", Value: "ascii"}},
	}
	data.SetData([]byte(sdp))
	offer.AddChild(data)
	call.AddChild(offer)
	iq.AddChild(call)
	return iq
}

func (m *Manager) sendTerminate() {
	m.mu.Lock()
	jid := m.jid
	callID := m.callID
	m.mu.Unlock()
	if callID == "" {
		return
	}
	iq := proto.NewNode("iq",
		proto.Attribute{Key: "id", Value: callID},
		proto.Attribute{Key: "type", Value: "set"},
		proto.Attribute{Key: "xmlns", Value: "call"},
		proto.Attribute{Key: "to", Value: jid},
	)
	call := proto.NewNode("call",
		proto.Attribute{Key: "call-id", Value: callID},
		proto.Attribute{Key: "call-creator", Value: creatorTag(callID)},
	)
	call.AddChild(&proto.Node{
		Tag: "terminate",
		Attrs: []proto.Attribute{
			{Key: "call-id", Value: callID},
		},
	})
	iq.AddChild(call)
	if err := m.sock.Send(iq); err != nil {
		log.Printf("[call] не удалось отправить terminate: %v", err)
	}
}

func (m *Manager) sendProps() {
	m.mu.Lock()
	jid := m.jid
	callID := m.callID
	muted := m.muted
	m.mu.Unlock()
	if callID == "" {
		return
	}
	iq := proto.NewNode("iq",
		proto.Attribute{Key: "id", Value: callID},
		proto.Attribute{Key: "type", Value: "set"},
		proto.Attribute{Key: "xmlns", Value: "call"},
		proto.Attribute{Key: "to", Value: jid},
	)
	call := proto.NewNode("call",
		proto.Attribute{Key: "call-id", Value: callID},
		proto.Attribute{Key: "call-creator", Value: creatorTag(callID)},
	)
	props := proto.NewNode("props",
		proto.Attribute{Key: "call-id", Value: callID},
		proto.Attribute{Key: "mute", Value: strconv.FormatBool(muted)},
		proto.Attribute{Key: "auto-mute", Value: "false"},
		proto.Attribute{Key: "conn-quality", Value: "unknown"},
		proto.Attribute{Key: "net-info", Value: "unknown"},
	)
	call.AddChild(props)
	iq.AddChild(call)
	if err := m.sock.Send(iq); err != nil {
		log.Printf("[call] не удалось отправить props: %v", err)
	}
}

func (m *Manager) HandleEvent(ev wa.CallEvent) string {
	m.mu.Lock()
	callID := m.callID
	jid := m.jid
	m.mu.Unlock()
	if ev.ID != "" && callID != "" && ev.ID != callID {
		return ""
	}
	if ev.From != "" && jid != "" && ev.From != jid {
		return ""
	}
	switch ev.Status {
	case "ringing":
		m.setState(StateRinging)
		return "дозвон"
	case "accept":
		m.setState(StateActive)
		m.flushPending()
		return "собеседник ответил"
	case "reject", "timeout", "terminate":
		m.cleanup()
		return "звонок завершён собеседником"
	default:
		return ""
	}
}

func (m *Manager) setState(s State) {
	m.mu.Lock()
	m.state = s
	cb := m.onState
	m.mu.Unlock()
	if cb != nil {
		cb(string(s))
	}
}

func (m *Manager) flushPending() {
	m.mu.Lock()
	pending := m.pending
	m.pending = ""
	m.mu.Unlock()
	if pending != "" {
		if _, err := m.Play(pending); err != nil {
			log.Printf("[call] не удалось запустить отложенный трек: %v", err)
		}
	}
}

func (m *Manager) Play(file string) (bool, error) {
	m.mu.Lock()
	if m.state == StateIdle {
		m.pending = file
		m.mu.Unlock()
		return false, nil
	}
	m.mu.Unlock()

	p := player.New(m.cfg.BitrateKbps, m.cfg.Channels)
	p.SetLoop(m.Loop())
	if err := p.Start(file); err != nil {
		return false, err
	}
	m.mu.Lock()
	m.player = p
	m.mediaPath = file
	m.mu.Unlock()
	m.sendProps()
	return true, nil
}

func (m *Manager) StopPlayer() {
	m.mu.Lock()
	p := m.player
	m.player = nil
	m.mediaPath = ""
	m.mu.Unlock()
	if p != nil {
		p.Stop()
	}
}

func (m *Manager) cleanup() {
	m.StopPlayer()
	m.mu.Lock()
	m.jid = ""
	m.callID = ""
	m.pending = ""
	m.state = StateIdle
	m.mu.Unlock()
}

func (m *Manager) Hangup() bool {
	m.mu.Lock()
	wasActive := m.state != StateIdle
	m.mu.Unlock()
	if !wasActive {
		return false
	}
	m.sendTerminate()
	m.cleanup()
	return true
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	st := m.state
	jid := m.jid
	loop := m.loop
	muted := m.muted
	p := m.player
	m.mu.Unlock()
	return Status{
		State:   string(st),
		JID:     jid,
		Playing: p != nil && p.Playing(),
		Loop:    loop,
		Muted:   muted,
	}
}

func (m *Manager) InCall() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state != StateIdle
}

func randomCallID() string {
	b := make([]byte, 8)
	for i := range b {
		b[i] = hexDigits[int(time.Now().UnixNano()+int64(i))%len(hexDigits)]
	}
	return strings.ToUpper(string(b))
}

const hexDigits = "0123456789ABCDEF"

func creatorTag(callID string) string {
	return callID + "@s.whatsapp.net"
}

func sdpPayload(callID string) string {
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	lines := []string{
		"v=0",
		"o=- " + now + " " + now + " IN IP4 127.0.0.1",
		"s=wa",
		"t=0 0",
		"c=IN IP4 0.0.0.0",
		"a=ice-options:google-ice",
		"m=audio 9 UDP/TLS/RTP/SAVPF 111",
		"c=IN IP4 0.0.0.0",
		"a=rtpmap:111 opus/48000/2",
		"a=fmtp:111 minptime=10;useinbandfec=1",
		"a=rtcp:9",
		"a=setup:active",
		"a=mid:0",
		"a=sendrecv",
		"a=ice-ufrag:wabaufrag",
		"a=ice-pwd:wabapasswordwabapassword1234",
		"a=fingerprint:sha-256 00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00",
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}
