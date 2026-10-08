import fs from "fs";
import path from "path";

const AUDIO_DIR = path.resolve("audio");
const ALLOWED_EXT = [".mp3", ".ogg", ".wav", ".m4a"];

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
