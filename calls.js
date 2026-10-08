import { spawn } from "child_process";

const CALL_ID = "waba-call-01";

export class CallManager {
  constructor(sock, config) {
    this.sock = sock;
    this.config = config || {};
    this.jid = null;
    this.callId = null;
    this.state = "idle";
    this.mediaPath = null;
    this.loop = false;
    this.muted = false;
    this.player = null;
    this.acceptTimeout = null;
  }

  buildOffer() {
    return {
      tag: "call",
      attrs: {
        id: CALL_ID,
        to: this.jid,
        from: this.sock.user.id,
      },
      content: [
        {
          tag: "offer",
          attrs: {
            "call-id": CALL_ID,
            "call-creator": this.sock.user.id,
            "from-prekey": "-1",
            cc_ver: "5",
            "device-face": "false",
            lt: "md",
            media: "audio",
            opus_dec_gains_ctl: "1",
            opus_enc_gain_ctl: "1",
            "prefer-hide-preview": "0",
            rtx_allowed: "true",
          },
          content: [
            {
              tag: "data",
              attrs: {
                encoding: "ascii",
              },
              content: Buffer.from(
                [
                  "v=0",
                  "o=- " + Date.now() + " " + Date.now() + " IN IP4 127.0.0.1",
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
                  "",
                ].join("\r\n")
              ).toString("base64"),
            },
          ],
        },
      ],
    };
  }

  async dial(jid) {
    if (this.state !== "idle") {
      throw new Error("звонок уже активен");
    }
    this.jid = jid;
    this.callId = CALL_ID;
    this.state = "dialing";
    await this.sock.query(this.buildOffer());
    this.acceptTimeout = setTimeout(() => {
      if (this.state === "dialing") {
        console.warn("[call] нет ответа на звонок, сброс состояния");
        this.hangup();
      }
    }, 60000);
    return this.jid;
  }

  handleEvent(call) {
    if (!call || call.id !== this.callId || call.from !== this.jid) {
      return null;
    }
    if (call.status === "ringing") {
      this.state = "ringing";
      return "дозвон";
    }
    if (call.status === "accept") {
      clearTimeout(this.acceptTimeout);
      this.state = "active";
      if (this.pendingFile) {
        this.play(this.pendingFile);
        this.pendingFile = null;
      }
      return "собеседник ответил";
    }
    if (
      call.status === "reject" ||
      call.status === "timeout" ||
      call.status === "terminate"
    ) {
      this.cleanup();
      return "звонок завершён собеседником";
    }
    return null;
  }

  play(file) {
    if (this.state === "idle") {
      this.pendingFile = file;
      return false;
    }
    this.stopPlayer();
    this.mediaPath = file;
    const bitrate = this.config.bitrate_kbps || 32;
    const args = [
      "-re",
      "-i", file,
      "-f", "ogg",
      "-c:a", "libopus",
      "-application", "voip",
      "-ar", "48000",
      "-ac", String(this.config.channels || 1),
      "-b:a", `${bitrate}k`,
      "-loglevel", "error",
      "pipe:1",
    ];
    try {
      this.player = spawn("ffmpeg", args);
    } catch (e) {
      console.warn("[call] не удалось запустить ffmpeg:", e.message);
      this.player = null;
      return false;
    }
    this.player.on("error", (e) => {
      console.warn("[call] ошибка процесса ffmpeg:", e.message);
      this.player = null;
    });
    this.player.on("close", (code) => {
      this.player = null;
      if (this.loop && code === 0 && this.state !== "idle") {
        this.play(this.mediaPath);
      }
    });
    this.sendCallProps();
    return true;
  }

  sendCallProps() {
    const stanza = {
      tag: "call",
      attrs: {
        id: CALL_ID,
        to: this.jid,
        from: this.sock.user.id,
      },
      content: [
        {
          tag: "props",
          attrs: {
            "call-id": this.callId,
            "call-creator": this.sock.user.id,
            mute: this.muted ? "true" : "false",
            "auto-mute": "false",
            "conn-quality": "unknown",
            "net-info": "unknown",
          },
        },
      ],
    };
    this.sock.query(stanza).catch((e) => {
      console.warn("[call] не удалось отправить props в звонок:", e.message);
    });
  }

  setMuted(value) {
    this.muted = value;
    if (this.state !== "idle") {
      this.sendCallProps();
    }
  }

  stopPlayer() {
    if (this.player) {
      try {
        this.player.kill("SIGKILL");
      } catch (e) {
        console.warn("[call] не удалось остановить плеер:", e.message);
      }
      this.player = null;
    }
  }

  cleanup() {
    clearTimeout(this.acceptTimeout);
    this.stopPlayer();
    this.jid = null;
    this.callId = null;
    this.state = "idle";
    this.mediaPath = null;
    this.pendingFile = null;
  }

  hangup() {
    if (this.state === "idle") {
      return false;
    }
    const jid = this.jid;
    const callId = this.callId;
    const stanza = {
      tag: "call",
      attrs: {
        id: CALL_ID,
        to: jid,
        from: this.sock.user.id,
      },
      content: [
        {
          tag: "terminate",
          attrs: {
            "call-id": callId,
            "call-creator": this.sock.user.id,
          },
        },
      ],
    };
    this.sock.query(stanza).catch((e) => {
      console.warn("[call] не удалось отправить terminate:", e.message);
    });
    this.cleanup();
    return true;
  }

  status() {
    return {
      state: this.state,
      jid: this.jid,
      playing: Boolean(this.player),
      loop: this.loop,
      muted: this.muted,
    };
  }
}
