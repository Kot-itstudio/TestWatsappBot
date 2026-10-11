package commands

import (
	"log"
	"regexp"
	"strings"

	"github.com/user/wabot/internal/audio"
	"github.com/user/wabot/internal/calls"
	"github.com/user/wabot/internal/config"
	"github.com/user/wabot/internal/menu"
	"github.com/user/wabot/internal/store"
	"github.com/user/wabot/internal/wa"
)

type Sender interface {
	SendText(to, text string) error
	SendPTT(to, path string) error
}

type Deps struct {
	Socket   *wa.Client
	Sender   Sender
	Config   *config.Config
	Calls    *calls.Manager
	Chats    *store.ChatStore
	AudioDir string
}

type Router struct {
	deps      Deps
	menus     map[string]*menu.State
	devMenus  map[string]*menu.State
	mainItems []string
	devItems  []string
}

var wsRe = regexp.MustCompile(`\s+`)

func NewRouter(deps Deps) *Router {
	r := &Router{
		deps:      deps,
		menus:     map[string]*menu.State{},
		devMenus:  map[string]*menu.State{},
		mainItems: []string{"Плей", "Стоп", "Луп", "Майд", "Микр", "Звони", "Выйти", "Файлы", "Статус", "Справочник"},
		devItems:  []string{"Очистить_состояние", "Широковещ", "Перезагрузка", "Конфиг", "Тест_звук", "Звонки", "Справочник"},
	}
	return r
}

func (r *Router) getMenu(chatID string) *menu.State {
	m, ok := r.menus[chatID]
	if !ok {
		m = menu.New(r.mainItems)
		r.menus[chatID] = m
	}
	return m
}

func (r *Router) getDevMenu(chatID string) *menu.State {
	m, ok := r.devMenus[chatID]
	if !ok {
		m = menu.New(r.devItems)
		r.devMenus[chatID] = m
	}
	return m
}

func (r *Router) isOwner(sender string) bool {
	return r.deps.Config.Owner != "" && sender == r.deps.Config.Owner
}

func (r *Router) send(chatID, text string) {
	if err := r.deps.Sender.SendText(chatID, text); err != nil {
		log.Printf("[send] ошибка отправки сообщения: %v", err)
	}
}

func (r *Router) HelpText() string {
	p := r.deps.Config.Prefix
	return strings.Join([]string{
		"Команды:",
		p + "меню / " + p + "menu — открыть меню",
		p + "меню вверх | " + p + "menu up — навигация",
		p + "меню вниз | " + p + "menu down — навигация",
		p + "меню <номер> | " + p + "menu <n> — выбрать пункт",
		p + "выбор / " + p + "accept — выполнить выбранный пункт",
		p + "плей <файл> / " + p + "play — играть аудио (в звонке идёт собеседнику)",
		p + "стоп / " + p + "stop — остановить плеер",
		p + "луп / " + p + "loop — повтор трека",
		p + "майд / " + p + "mute — мут микрофона",
		p + "микр / " + p + "unmute — снять мут",
		p + "звони <номер> [файл] / " + p + "call — позвонить и играть аудио",
		p + "выйти / " + p + "hangup — завершить звонок",
		p + "файлы / " + p + "files — список аудио",
		p + "статус / " + p + "status — состояние",
		p + "справочник / " + p + "help — эта справка",
		p + "разраб / " + p + "dev — меню разработчика (owner)",
	}, "\n")
}

func NormalizePhone(input string) string {
	digits := make([]rune, 0, len(input))
	for _, c := range input {
		if c >= '0' && c <= '9' {
			digits = append(digits, c)
		}
	}
	if len(digits) >= 10 {
		return string(digits)
	}
	return ""
}

func (r *Router) HandleMessage(msg wa.Message) {
	if msg.RemoteJID == "" || strings.Contains(msg.RemoteJID, "@broadcast") {
		return
	}
	r.deps.Chats.Add(msg.RemoteJID)
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	r.HandleCommand(msg.RemoteJID, text)
}

func (r *Router) HandleCommand(chatID, body string) {
	p := r.deps.Config.Prefix
	if !strings.HasPrefix(body, p) {
		return
	}
	fields := wsRe.Split(strings.TrimSpace(body[len(p):]), -1)
	if len(fields) == 0 || fields[0] == "" {
		return
	}
	cmd := strings.ToLower(fields[0])
	args := fields[1:]
	switch cmd {
	case "меню", "menu":
		r.handleMenu(chatID, args)
	case "выбор", "accept":
		r.handleAccept(chatID)
	case "плей", "play":
		r.handlePlay(chatID, args)
	case "стоп", "stop":
		r.handleStop(chatID)
	case "луп", "loop":
		r.handleLoop(chatID)
	case "майд", "mute":
		r.handleMute(chatID, true)
	case "микр", "unmute":
		r.handleMute(chatID, false)
	case "звони", "call":
		r.handleCall(chatID, args)
	case "выйти", "hangup":
		r.handleHangup(chatID)
	case "файлы", "files":
		r.handleFiles(chatID)
	case "статус", "status":
		r.handleStatus(chatID)
	case "помощь", "справочник", "help":
		r.send(chatID, r.HelpText())
	case "разраб", "dev":
		r.handleDev(chatID, args)
	default:
	}
}

func (r *Router) handleMenu(chatID string, parts []string) {
	m := r.getMenu(chatID)
	sub := ""
	if len(parts) > 0 {
		sub = strings.ToLower(parts[0])
	}
	if sub == "" {
		r.send(chatID, m.Render())
		return
	}
	if sub == "вверх" || sub == "up" {
		m.Up()
		r.send(chatID, m.Render())
		return
	}
	if sub == "вниз" || sub == "down" {
		m.Down()
		r.send(chatID, m.Render())
		return
	}
	if _, ok := m.Pick(sub); !ok {
		r.send(chatID, "Неверный номер пункта")
		return
	}
	r.send(chatID, m.Render())
}

func (r *Router) handleAccept(chatID string) {
	m := r.getMenu(chatID)
	r.runMenuItem(chatID, m.Current())
}

func (r *Router) runMenuItem(chatID, item string) {
	p := r.deps.Config.Prefix
	switch item {
	case "Плей":
		r.send(chatID, "Используйте "+p+"плей <файл>")
	case "Стоп":
		r.handleStop(chatID)
	case "Луп":
		r.handleLoop(chatID)
	case "Майд":
		r.handleMute(chatID, true)
	case "Микр":
		r.handleMute(chatID, false)
	case "Звони":
		r.send(chatID, "Используйте "+p+"звони <номер> [файл]")
	case "Выйти":
		r.handleHangup(chatID)
	case "Файлы":
		r.handleFiles(chatID)
	case "Статус":
		r.handleStatus(chatID)
	case "Справочник":
		r.send(chatID, r.HelpText())
	default:
		r.send(chatID, "Неизвестный пункт меню")
	}
}

func (r *Router) handleDev(chatID string, parts []string) {
	if !r.isOwner(chatID) {
		r.send(chatID, "Команда не найдена")
		return
	}
	m := r.getDevMenu(chatID)
	sub := ""
	if len(parts) > 0 {
		sub = strings.ToLower(parts[0])
	}
	if sub == "" {
		r.send(chatID, m.Render())
		return
	}
	if sub == "вверх" || sub == "up" {
		m.Up()
		r.send(chatID, m.Render())
		return
	}
	if sub == "вниз" || sub == "down" {
		m.Down()
		r.send(chatID, m.Render())
		return
	}
	if isNumeric(sub) {
		if _, ok := m.Pick(sub); !ok {
			r.send(chatID, "Неверный номер пункта")
			return
		}
		r.send(chatID, m.Render())
		return
	}
	if sub == "выполнить" || sub == "run" {
		r.runDevItem(chatID, m.Current(), parts[1:])
		return
	}
	alias := map[string]string{
		"очистить_состояние": "Очистить_состояние",
		"широковещ":          "Широковещ",
		"перезагрузка":       "Перезагрузка",
		"конфиг":             "Конфиг",
		"тест_звук":          "Тест_звук",
		"звонки":             "Звонки",
		"справочник":         "Справочник",
	}
	if mapped, ok := alias[sub]; ok {
		r.runDevItem(chatID, mapped, parts[1:])
		return
	}
	r.send(chatID, "Неизвестная dev-команда")
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (r *Router) runDevItem(chatID, item string, args []string) {
	switch item {
	case "Очистить_состояние":
		delete(r.menus, chatID)
		delete(r.devMenus, chatID)
		if r.deps.Calls != nil {
			r.deps.Calls.StopPlayer()
			r.deps.Calls.SetMuted(false)
		}
		r.send(chatID, "Состояние очищено")
	case "Широковещ":
		text := strings.TrimSpace(strings.Join(args, " "))
		if text == "" {
			r.send(chatID, "Укажите текст для рассылки")
			return
		}
		r.broadcast(text)
	case "Перезагрузка":
		r.send(chatID, "Перезапуск подключения...")
		if err := r.deps.Socket.Logout(); err != nil {
			log.Printf("[dev] ошибка logout при перезагрузке: %v", err)
		}
	case "Конфиг":
		r.send(chatID, marshalJSONSafe(r.deps.Config))
	case "Тест_звук":
		r.handlePlay(chatID, []string{"test.mp3"})
	case "Звонки":
		if r.deps.Calls != nil {
			r.send(chatID, "Состояние звонка: "+r.deps.Calls.Status().State)
		} else {
			r.send(chatID, "Состояние звонка: нет")
		}
	case "Справочник":
		r.send(chatID, r.HelpText())
	default:
		r.send(chatID, "Неизвестный dev-пункт")
	}
}

func (r *Router) broadcast(text string) {
	chats := r.deps.Chats.Read()
	if len(chats) == 0 {
		log.Printf("[broadcast] нет сохранённых чатов")
		return
	}
	sent := 0
	for _, jid := range chats {
		if err := r.deps.Sender.SendText(jid, text); err != nil {
			log.Printf("[broadcast] не отправлено в %s: %v", jid, err)
			continue
		}
		sent++
	}
	log.Printf("[broadcast] отправлено в %d/%d чатов", sent, len(chats))
}

func (r *Router) handlePlay(chatID string, parts []string) {
	if r.deps.Calls == nil {
		r.send(chatID, "Сервис звонков не готов")
		return
	}
	files := audio.List(r.deps.AudioDir)
	name := ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if name == "" {
		if len(files) == 0 {
			r.send(chatID, "Папка audio пуста")
			return
		}
		name = files[0]
	}
	resolved, err := audio.Resolve(r.deps.AudioDir, name)
	if err != nil {
		r.send(chatID, "Ошибка: "+err.Error())
		return
	}
	inCall := r.deps.Calls.InCall()
	started, perr := r.deps.Calls.Play(resolved.Path)
	if perr != nil {
		r.send(chatID, "Не удалось запустить плеер: "+perr.Error())
		return
	}
	if inCall {
		if started {
			r.send(chatID, "Играет в звонок: "+resolved.Name)
		} else {
			r.send(chatID, "Трек отложен до ответа на звонок")
		}
	} else {
		r.send(chatID, "Играет: "+resolved.Name)
		if err := r.deps.Sender.SendPTT(chatID, resolved.Path); err != nil {
			log.Printf("[send] ошибка отправки PTT: %v", err)
		}
	}
	if r.deps.Config.EasterEggFile != "" && resolved.Name == r.deps.Config.EasterEggFile {
		r.send(chatID, "🥚 СЕКРЕТНАЯ ПАСХАЛКА!")
		if err := r.deps.Sender.SendPTT(chatID, resolved.Path); err != nil {
			log.Printf("[send] ошибка отправки PTT: %v", err)
		}
	}
}

func (r *Router) handleStop(chatID string) {
	if r.deps.Calls != nil {
		r.deps.Calls.StopPlayer()
	}
	r.send(chatID, "Плеер остановлен")
}

func (r *Router) handleLoop(chatID string) {
	if r.deps.Calls == nil {
		r.send(chatID, "Сервис звонков не готов")
		return
	}
	v := r.deps.Calls.ToggleLoop()
	if v {
		r.send(chatID, "Повтор трека: включён")
	} else {
		r.send(chatID, "Повтор трека: выключен")
	}
}

func (r *Router) handleMute(chatID string, value bool) {
	if r.deps.Calls != nil {
		r.deps.Calls.SetMuted(value)
	}
	if value {
		r.send(chatID, "Микрофон заглушён")
	} else {
		r.send(chatID, "Микрофон включён")
	}
}

func (r *Router) handleFiles(chatID string) {
	files := audio.List(r.deps.AudioDir)
	if len(files) == 0 {
		r.send(chatID, "Папка audio пуста")
		return
	}
	var sb strings.Builder
	for i, f := range files {
		sb.WriteString(itoa(i+1) + ". " + f + "\n")
	}
	r.send(chatID, strings.TrimRight(sb.String(), "\n"))
}

func (r *Router) handleStatus(chatID string) {
	st := calls.Status{State: "disabled"}
	if r.deps.Calls != nil {
		st = r.deps.Calls.Status()
	}
	lines := []string{
		"Подключение: " + r.deps.Socket.State().String(),
		"Звонок: " + st.State,
	}
	if st.JID != "" {
		lines = append(lines, "Собеседник: "+st.JID)
	}
	lines = append(lines, yesNo("Играет: ", st.Playing), yesNo("Луп: ", st.Loop), yesNo("Мут: ", st.Muted))
	r.send(chatID, strings.Join(lines, "\n"))
}

func yesNo(label string, v bool) string {
	if v {
		return label + "да"
	}
	return label + "нет"
}

func (r *Router) handleCall(chatID string, parts []string) {
	phone := ""
	if len(parts) > 0 {
		phone = NormalizePhone(parts[0])
	}
	if phone == "" {
		r.send(chatID, "Укажите номер телефона")
		return
	}
	if r.deps.Calls == nil {
		r.send(chatID, "Сервис звонков не готов")
		return
	}
	jid := phone + "@s.whatsapp.net"
	file := ""
	if len(parts) > 1 {
		resolved, err := audio.Resolve(r.deps.AudioDir, parts[1])
		if err != nil {
			r.send(chatID, "Ошибка: "+err.Error())
			return
		}
		file = resolved.Path
	}
	if err := r.deps.Calls.Dial(jid); err != nil {
		log.Printf("[call] ошибка звонка: %v", err)
		r.send(chatID, "Не удалось позвонить: "+err.Error())
		return
	}
	if file != "" {
		if _, err := r.deps.Calls.Play(file); err != nil {
			log.Printf("[call] не удалось запустить трек в звонок: %v", err)
		}
	}
	r.send(chatID, "Звоним: "+phone)
}

func (r *Router) handleHangup(chatID string) {
	if r.deps.Calls != nil {
		r.deps.Calls.Hangup()
	}
	r.send(chatID, "Звонок завершён")
}
