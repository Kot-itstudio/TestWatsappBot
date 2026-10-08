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
    this.coreSock = null;
    this.client = null;
    this.caller = null;
    this.peerJid = null;
    this.player = null;
    this.currentFile = null;
    this.loop = false;
    this.muted = false;
    this.streamAttached = false;
  }

  async connect() {
    if (!wavoip) {
      throw new Error(wavoipError || "voice-calls-baileys недоступна");
    }
    if (this.client) {
      return this.client;
    }
    this.client = new wavoip(this.coreSock, {
      token: this.config.wavoip_token,
      software_name: this.config.software_name,
      sample_rate: this.config.sample_rate || 48000,
      channels: this.config.channels || 1,
    });
    await this.client.connect();
    this.bindCallEvents();
    return this.client;
  }

  bindCallEvents() {
    if (!this.client || typeof this.client.on !== "function") {
      return;
    }
    this.client.on("call:update", (call) => {
      const state = call?.state || call?.status;
      if (state === "connect" || state === "active") {
        this.attachPlayerStream();
      }
      if (state === "end" || state === "ended" || state === "hangup") {
        this.caller = null;
        this.peerJid = null;
        this.streamAttached = false;
        this.stopPlayer();
      }
    });
  }

  async dial(jid) {
    if (this.client) {
      this.peerJid = jid;
    } else {
      await this.connect();
      this.peerJid = jid;
    }
    let link = null;
    let caller = null;
    if (typeof this.client.createCallLink === "function") {
      link = await this.client.createCallLink(jid);
    }
    if (typeof this.client.call === "function") {
      caller = link
        ? await this.client.call(link, jid)
        : await this.client.call(jid);
    } else if (typeof this.client.callFromLink === "function" && link) {
      caller = await this.client.callFromLink(link, jid);
    } else {
      throw new Error("у Wavoip нет метода дозвона (call/callFromLink)");
    }
    this.caller = caller;
    this.attachPlayerStream();
    return link || jid;
  }

  attachPlayerStream() {
    if (!this.player || !this.client) {
      return false;
    }
    const stream = this.player.stdout;
    const targets = [this.caller, this.client];
    const methods = ["addMediaStream", "setMicrophoneStream", "injectStream"];
    for (const target of targets) {
      if (!target) continue;
      for (const method of methods) {
        if (typeof target[method] === "function") {
          try {
            target[method](stream);
            this.streamAttached = true;
            return true;
          } catch (e) {
            console.warn(`[voip] ${method} не сработал:`, e.message);
          }
        }
      }
    }
    console.warn("[voip] не найден метод подачи PCM в медиа-мост, звук в звонок не пошёл");
    return false;
  }

  startPlayer(file) {
    this.stopPlayer();
    this.currentFile = file;
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
      this.loop = false;
      this.player = null;
    });
    this.player.stdout.on("error", (e) => {
      console.warn("[voip] ошибка чтения PCM-потока:", e.message);
    });
    this.player.stderr.on("data", (chunk) => {
      const msg = chunk.toString().trim();
      if (msg) {
        console.warn(`[voip] ffmpeg: ${msg}`);
      }
    });
    this.player.on("close", (code) => {
      this.player = null;
      this.streamAttached = false;
      if (code !== 0) {
        console.warn(`[voip] ffmpeg завершился с кодом ${code}, повтор отключён`);
        this.loop = false;
        return;
      }
      if (this.loop && this.currentFile) {
        this.startPlayer(this.currentFile);
      }
    });
    this.attachPlayerStream();
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
      this.streamAttached = false;
    }
  }

  hangup() {
    this.stopPlayer();
    try {
      if (this.caller && typeof this.caller.hangup === "function") {
        this.caller.hangup();
      } else if (typeof this.client?.hangup === "function") {
        this.client.hangup();
      }
    } catch (e) {
      console.warn("[voip] ошибка при завершении звонка:", e.message);
    }
    this.caller = null;
    this.peerJid = null;
    this.streamAttached = false;
  }

  status() {
    return {
      available: Boolean(wavoip),
      connected: Boolean(this.client),
      in_call: Boolean(this.caller),
      peer: this.peerJid,
      playing: Boolean(this.player),
      stream_attached: this.streamAttached,
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
