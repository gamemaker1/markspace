// Generates the seed files: PNG images, a multi-page PDF, CSV text and a
// short video. Nothing is read from outside the repository, except ffmpeg
// for the video when it is installed.
import { crc32, deflateSync } from "node:zlib";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

/** A width x height RGB PNG with a colour gradient chosen by seed. */
export function png(width, height, seed = 1) {
  const rows = [];
  for (let y = 0; y < height; y++) {
    const row = Buffer.alloc(1 + width * 3);
    for (let x = 0; x < width; x++) {
      // Blue and fuchsia tones only, so the colour check has no image yellow to skip.
      row[1 + x * 3] = (40 + ((x * 180) / width + seed * 30)) & 0xff;
      row[2 + x * 3] = (30 + (y * 60) / height) & 0xff;
      row[3 + x * 3] = (160 + (seed * 20) % 90) & 0xff;
    }
    rows.push(row);
  }
  const chunk = (type, data) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const td = Buffer.concat([Buffer.from(type), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(crc32(td) >>> 0);
    return Buffer.concat([len, td, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(Buffer.concat(rows))), chunk("IEND", Buffer.alloc(0))]);
}

/** A PDF with the given number of text pages. */
export function pdf(pages = 6, title = "Harbour report") {
  const objects = [];
  const add = (body) => {
    objects.push(body);
    return objects.length;
  };
  const catalog = add("");
  const pagesObj = add("");
  const font = add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>");
  const kids = [];
  for (let i = 1; i <= pages; i++) {
    const lines = [`${title}, page ${i}`, "", "The tide gauge readings were taken at low water.", `Section ${i} covers the north quay and the grain terminal.`, "Silt depth rose 18 cm since spring."];
    const text = lines.map((l, n) => `BT /F1 ${n === 0 ? 20 : 12} Tf 72 ${720 - n * 24} Td (${l.replace(/[()\\]/g, "")}) Tj ET`).join("\n");
    const content = add(`<< /Length ${Buffer.byteLength(text)} >>\nstream\n${text}\nendstream`);
    kids.push(add(`<< /Type /Page /Parent ${pagesObj} 0 R /MediaBox [0 0 612 792] /Contents ${content} 0 R /Resources << /Font << /F1 ${font} 0 R >> >> >>`));
  }
  objects[catalog - 1] = `<< /Type /Catalog /Pages ${pagesObj} 0 R >>`;
  objects[pagesObj - 1] = `<< /Type /Pages /Kids [${kids.map((k) => `${k} 0 R`).join(" ")}] /Count ${kids.length} >>`;
  let out = "%PDF-1.4\n";
  const offsets = [];
  objects.forEach((body, i) => {
    offsets.push(Buffer.byteLength(out));
    out += `${i + 1} 0 obj\n${body}\nendobj\n`;
  });
  const xref = Buffer.byteLength(out);
  out += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const o of offsets) out += `${String(o).padStart(10, "0")} 00000 n \n`;
  out += `trailer\n<< /Size ${objects.length + 1} /Root ${catalog} 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(out, "latin1");
}

/** A CSV of rows readings with a quoted notes column. */
export function csv(rows = 80) {
  const out = ["site,depth_m,reading,notes"];
  for (let i = 1; i <= rows; i++) out.push(`Pier ${i},${(i * 0.37).toFixed(2)},${1000 + i * 7},"note ${i}, checked at low tide"`);
  return Buffer.from(out.join("\n") + "\n");
}

/**
 * A 2-second MP4 made with ffmpeg in dir. Returns null when ffmpeg is
 * missing or fails. UIAUDIT_VIDEO may name an existing file instead.
 */
export function video(dir) {
  if (process.env.UIAUDIT_VIDEO && existsSync(process.env.UIAUDIT_VIDEO)) return readFileSync(process.env.UIAUDIT_VIDEO);
  const file = join(dir, "tide.mp4");
  for (const codec of [["-c:v", "libx264", "-pix_fmt", "yuv420p"], ["-c:v", "mpeg4"]]) {
    try {
      execFileSync("ffmpeg", ["-y", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=10", ...codec, "-movflags", "+faststart", file], { stdio: "ignore", timeout: 30000 });
      if (existsSync(file)) return readFileSync(file);
    } catch {
      /* try the next codec */
    }
  }
  return null;
}
