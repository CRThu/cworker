// web/src/lib/utils/ansi.ts - 轻量高效 ANSI 16色终端转义序列转 HTML 解析器

const ANSI_COLORS: Record<number, string> = {
  30: '#1e293b', // Black
  31: '#f87171', // Red
  32: '#4ade80', // Green
  33: '#facc15', // Yellow
  34: '#60a5fa', // Blue
  35: '#c084fc', // Magenta
  36: '#38bdf8', // Cyan
  37: '#f1f5f9', // White
  90: '#64748b', // Bright Black
  91: '#ef4444', // Bright Red
  92: '#22c55e', // Bright Green
  93: '#eab308', // Bright Yellow
  94: '#3b82f6', // Bright Blue
  95: '#a855f7', // Bright Magenta
  96: '#06b6d4', // Bright Cyan
  97: '#ffffff', // Bright White
};

export function escapeHtml(str: string): string {
  if (!str) return '';
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

export function ansiToHtml(raw: string): string {
  if (!raw) return '';
  const parts = raw.split(/\x1b\[([0-9;]*)m/);
  let html = '';
  let curColor: string | null = null;
  let isBold = false;

  for (let i = 0; i < parts.length; i++) {
    if (i % 2 === 1) {
      const codeStr = parts[i];
      if (!codeStr || codeStr === '0') {
        curColor = null;
        isBold = false;
      } else {
        const codes = codeStr.split(';').map(c => parseInt(c, 10));
        for (const code of codes) {
          if (code === 1) isBold = true;
          else if (code === 22) isBold = false;
          else if (ANSI_COLORS[code]) curColor = ANSI_COLORS[code];
        }
      }
    } else {
      const text = escapeHtml(parts[i]);
      if (text) {
        let style = '';
        if (curColor) style += `color: ${curColor};`;
        if (isBold) style += 'font-weight: bold;';
        if (style) {
          html += `<span style="${style}">${text}</span>`;
        } else {
          html += text;
        }
      }
    }
  }

  return html;
}
