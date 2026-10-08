import { spawn } from "child_process";
import fs from "fs";
import path from "path";

const AUDIO_DIR = path.resolve("audio");
const ALLOWED_EXT = [".mp3", ".ogg", ".wav", ".m4a"];

let wavoip = null;
let wavoipError = null;

try {
  const mod = await import("voice-calls-baileys");
  wavoip = mod.Wavoip || mod.default?.Wavoip || null;
  if (!wavoip) {
    wavoipError = "не найден экспорт Wavoip";
  }
} catch (e) {
  wavoipError = e.message;
  console.warn("[voip] voice-calls-baileys не подключена:", e.message);
}

export function isVoipAvailable() {
  return Boolean(wavoip);
}

export function voipInfo() {
  if (!wavoip) {
    return { available: false, error: wavoipError };
  }
  return { available: true, error: null };
}

export function resolveAudioFile(name) {
  const safeName = path.basename(String(name || ""));
  const ext = path.extname(safeName).toLowerCase();
  if (!ALLOWED_EXT.includes(ext)) {
    return { ok: false, reason: "недопустимое расширение файла" };
  }
  const full = path.join(AUDIO_DIR, safeName);
  if (!full.startsWith(AUDIO_DIR + path.sep)) {
    return { ok: false, reason: "выход за пределы папки audio" };
  }
  if (!fs.existsSync(full)) {
    return { ok: false, reason: "файл не найден" };
  }
  return { ok: true, file: full, name: safeName };
}

export function listAudioFiles() {
  try {
    if (!fs.existsSync(AUDIO_DIR)) {
      return [];
    }
    return fs
      .readdirSync(AUDIO_DIR)
      .filter((f) => ALLOWED_EXT.includes(path.extname(f).toLowerCase()));
  } catch (e) {
    console.warn("[voip] не удалось прочитать папку audio:", e.message);
    return [];
  }
}

export class VoipSession {
  constructor(voipConfig) {
    this.config = voipConfig;
    this.client = null;
    this.rc13Sock = null;
    this.caller = null;
    this.player = null;
    this.loop = false;
    this.muted = false;
    this.active = false;
  }

  async connect(coreSock) {
    if (!wavoip) {
      throw new Error(wavoipError || "voice-calls-baileys недоступна");
    }
    let sessionSock = coreSock;
    try {
      const baileysRc13 = await import("baileys");
      const authDir = path.resolve("data", "auth-rc13");
      if (!fs.existsSync(authDir)) {
        fs.mkdirSync(authDir, { recursive: true });
      }
      const rc13Auth = await baileysRc13.useMultiFileAuthState(authDir);
      this.rc13Sock = baileysRc13.makeWASocket({
        auth: rc13Auth.state,
        printQRInTerminal: false,
      });
      this.rc13Sock.ev.on("connection.update", (u) => {
        if (u.connection === "open") {
          console.log("[voip] rc13-сессия открыта");
        }
      });
      sessionSock = this.rc13Sock;
    } catch (e) {
      console.warn("[voip] не удалось поднять отдельную rc13-сессию, использую ядро:", e.message);
    }
    this.client = new wavoip(sessionSock, {
      token: this.config.wavoip_token,
      software_name: this.config.software_name,
    });
    await this.client.connect();
    this.active = true;
    return this.client;
  }

  async dial(jid) {
    if (!this.client) {
      throw new Error("VoIP-сессия не подключена");
    }
    const link = await this.client.createCallLink(jid);
    this.caller = await this.client.callFromLink(link, jid);
    return link;
  }

  attachPlayerStream() {
    if (!this.player || !this.caller) {
      return false;
    }
    try {
      if (typeof this.caller.addMediaStream === "function") {
        this.caller.addMediaStream(this.player.stdout);
        return true;
      }
      if (typeof this.client?.addMediaStream === "function") {
        this.client.addMediaStream(this.player.stdout);
        return true;
      }
      console.warn("[voip] у Wavoip нет метода addMediaStream, PCM в медиа-мост не передан");
      return false;
    } catch (e) {
      console.warn("[voip] не удалось подключить плеер к медиа-мосту:", e.message);
      return false;
    }
  }

  startPlayer(file) {
    this.stopPlayer();
    const args = [
      "-i", file,
      "-f", "s16le",
      "-ar", String(this.config.sample_rate || 48000),
      "-ac", String(this.config.channels || 1),
      "-loglevel", "error",
      "pipe:1",
    ];
    try {
      this.player = spawn("ffmpeg", args);
    } catch (e) {
      console.warn("[voip] не удалось запустить ffmpeg:", e.message);
      this.player = null;
      return null;
    }
    this.player.on("error", (e) => {
      console.warn("[voip] ошибка процесса ffmpeg:", e.message);
      this.player = null;
    });
    this.player.on("close", () => {
      this.player = null;
      if (this.loop) {
        this.startPlayer(file);
      }
    });
    return this.player;
  }

  stopPlayer() {
    if (this.player) {
      try {
        this.player.kill("SIGKILL");
      } catch (e) {
        console.warn("[voip] не удалось остановить плеер:", e.message);
      }
      this.player = null;
    }
  }

  hangup() {
    this.stopPlayer();
    try {
      if (this.caller && typeof this.caller.hangup === "function") {
        this.caller.hangup();
      }
    } catch (e) {
      console.warn("[voip] ошибка при завершении звонка:", e.message);
    }
    this.caller = null;
    this.active = false;
  }

  status() {
    return {
      available: Boolean(wavoip),
      connected: Boolean(this.client),
      in_call: Boolean(this.caller),
      playing: Boolean(this.player),
      loop: this.loop,
      muted: this.muted,
    };
  }
}

export function encodePttToOgg(inputFile, bitrateKbps = 32) {
  return new Promise((resolve, reject) => {
    const out = inputFile + ".ptt.ogg";
    const args = [
      "-y",
      "-i", inputFile,
      "-c:a", "libopus",
      "-application", "voip",
      "-ar", "48000",
      "-ac", "1",
      "-b:a", `${bitrateKbps}k`,
      out,
    ];
    let proc;
    try {
      proc = spawn("ffmpeg", args);
    } catch (e) {
      reject(e);
      return;
    }
    proc.on("error", reject);
    proc.on("close", (code) => {
      if (code === 0) {
        resolve(out);
      } else {
        reject(new Error(`ffmpeg завершился с кодом ${code}`));
      }
    });
  });
}
