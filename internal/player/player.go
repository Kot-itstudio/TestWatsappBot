package player

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
)

type Player struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdout   io.ReadCloser
	running  bool
	loop     bool
	file     string
	bitrate  int
	channels int
	onDone   func()
}

func New(bitrate, channels int) *Player {
	return &Player{bitrate: bitrate, channels: channels}
}

func (p *Player) SetLoop(v bool) {
	p.mu.Lock()
	p.loop = v
	p.mu.Unlock()
}

func (p *Player) Loop() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loop
}

func (p *Player) Playing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *Player) OnDone(fn func()) {
	p.mu.Lock()
	p.onDone = fn
	p.mu.Unlock()
}

func (p *Player) Start(file string) error {
	p.mu.Lock()
	if p.running {
		p.stopLocked()
	}
	p.file = file
	p.mu.Unlock()

	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		log.Printf("[player] ffmpeg не найден в PATH: %v", err)
		return errors.New("ffmpeg не установлен")
	}
	args := []string{
		"-re",
		"-i", file,
		"-f", "ogg",
		"-c:a", "libopus",
		"-application", "voip",
		"-ar", "48000",
		"-ac", fmt.Sprintf("%d", p.channels),
		"-b:a", fmt.Sprintf("%dk", p.bitrate),
		"-loglevel", "error",
		"pipe:1",
	}
	cmd := exec.Command(path, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("не удалось создать pipe ffmpeg: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("не удалось запустить ffmpeg: %w", err)
	}

	p.mu.Lock()
	p.cmd = cmd
	p.stdout = stdout
	p.running = true
	p.mu.Unlock()

	go p.wait(cmd)
	return nil
}

func (p *Player) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	loop := false
	var onDone func()
	p.mu.Lock()
	if p.cmd == cmd {
		p.running = false
		p.cmd = nil
		p.stdout = nil
		loop = p.loop
		onDone = p.onDone
	}
	p.mu.Unlock()
	if err != nil {
		log.Printf("[player] ffmpeg завершился с ошибкой: %v", err)
	}
	if loop && err == nil {
		p.mu.Lock()
		file := p.file
		p.mu.Unlock()
		if serr := p.Start(file); serr != nil {
			log.Printf("[player] не удалось перезапустить трек по лупу: %v", serr)
		}
		return
	}
	if onDone != nil {
		onDone()
	}
}

func (p *Player) Read(b []byte) (int, error) {
	p.mu.Lock()
	r := p.stdout
	p.mu.Unlock()
	if r == nil {
		return 0, io.EOF
	}
	return r.Read(b)
}

func (p *Player) Stop() {
	p.mu.Lock()
	p.stopLocked()
	p.mu.Unlock()
}

func (p *Player) stopLocked() {
	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Kill(); err != nil {
			log.Printf("[player] не удалось остановить ffmpeg: %v", err)
		}
	}
	p.running = false
	p.cmd = nil
	p.stdout = nil
}
