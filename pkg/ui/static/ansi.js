// ansi.js - 极简 ANSI 转义字符着色转换器 (零外部依赖)
function ansiToHtml(text) {
    if (!text) return '';

    // 1. HTML 转义防御 XSS
    const htmlEscaped = text
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');

    // 2. ANSI 基础色彩映射表 (对齐标准 16 色终端规范)
    const colorMap = {
        '30': 'color: #4b5563', // Black / Gray
        '31': 'color: #ef4444', // Red
        '32': 'color: #22c55e', // Green
        '33': 'color: #eab308', // Yellow
        '34': 'color: #3b82f6', // Blue
        '35': 'color: #a855f7', // Magenta
        '36': 'color: #06b6d4', // Cyan
        '37': 'color: #f3f4f6', // White
        '90': 'color: #6b7280', // Bright Black (Gray)
        '91': 'color: #f87171', // Bright Red
        '92': 'color: #4ade80', // Bright Green
        '93': 'color: #facc15', // Bright Yellow
        '94': 'color: #60a5fa', // Bright Blue
        '95': 'color: #c084fc', // Bright Magenta
        '96': 'color: #22d3ee', // Bright Cyan
        '97': 'color: #ffffff', // Bright White
        '1': 'font-weight: 600', // Bold
        '2': 'opacity: 0.7',     // Dim
        '4': 'text-decoration: underline' // Underline
    };

    let openSpans = 0;
    const result = htmlEscaped.replace(/\x1b\[([0-9;]*)m/g, (match, codes) => {
        if (!codes || codes === '0') {
            // 重置全部样式
            const closes = '</span>'.repeat(openSpans);
            openSpans = 0;
            return closes;
        }

        const parts = codes.split(';');
        const styles = [];
        for (const code of parts) {
            if (colorMap[code]) {
                styles.push(colorMap[code]);
            }
        }

        if (styles.length > 0) {
            openSpans++;
            return `<span style="${styles.join('; ')}">`;
        }
        return '';
    });

    return result + '</span>'.repeat(openSpans);
}

if (typeof module !== 'undefined' && module.exports) {
    module.exports = { ansiToHtml };
}
