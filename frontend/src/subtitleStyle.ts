import type { CSSProperties } from "react";

export type SubSize = "sm" | "md" | "lg" | "xl";
export type SubColor = "white" | "ivory" | "yellow" | "cyan";
export type SubBg = "translucent" | "solid" | "outline";
export type SubFont = "sans" | "serif" | "mono";
export type SubPos = "low" | "mid" | "high";

export interface SubtitleStyle {
  size: SubSize;
  color: SubColor;
  bg: SubBg;
  font: SubFont;
  position: SubPos;
}

const STORAGE_KEY = "bigflix-subtitle-style-v1";

export const DEFAULT_SUBTITLE_STYLE: SubtitleStyle = {
  size: "md",
  color: "white",
  bg: "translucent",
  font: "sans",
  position: "low",
};

export const SUB_COLORS: Record<SubColor, { label: string; hex: string }> = {
  white: { label: "White", hex: "#ffffff" },
  ivory: { label: "Warm Ivory", hex: "#f5f2eb" },
  yellow: { label: "Cinema Yellow", hex: "#ffd95a" },
  cyan: { label: "Soft Cyan", hex: "#8be9fd" },
};

const SIZE_MAP: Record<SubSize, string> = {
  sm: "clamp(12px, 1.8vw, 17px)",
  md: "clamp(14px, 2.3vw, 22px)",
  lg: "clamp(17px, 2.9vw, 28px)",
  xl: "clamp(20px, 3.6vw, 35px)",
};

const FONT_MAP: Record<SubFont, string> = {
  sans: "var(--font-sans)",
  serif: 'Georgia, "Times New Roman", serif',
  mono: "var(--font-mono)",
};

const POS_MAP: Record<SubPos, string> = {
  low: "6%",
  mid: "13%",
  high: "22%",
};

export function loadSubtitleStyle(): SubtitleStyle {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULT_SUBTITLE_STYLE;
    const parsed = JSON.parse(raw) as Partial<SubtitleStyle>;
    return {
      size: parsed.size && parsed.size in SIZE_MAP ? parsed.size : DEFAULT_SUBTITLE_STYLE.size,
      color:
        parsed.color && parsed.color in SUB_COLORS ? parsed.color : DEFAULT_SUBTITLE_STYLE.color,
      bg:
        parsed.bg === "translucent" || parsed.bg === "solid" || parsed.bg === "outline"
          ? parsed.bg
          : DEFAULT_SUBTITLE_STYLE.bg,
      font: parsed.font && parsed.font in FONT_MAP ? parsed.font : DEFAULT_SUBTITLE_STYLE.font,
      position:
        parsed.position && parsed.position in POS_MAP
          ? parsed.position
          : DEFAULT_SUBTITLE_STYLE.position,
    };
  } catch {
    return DEFAULT_SUBTITLE_STYLE;
  }
}

export function saveSubtitleStyle(style: SubtitleStyle): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(style));
  } catch {
    // Ignore storage errors.
  }
}

export function cueContainerStyle(style: SubtitleStyle, uiOpen: boolean): CSSProperties {
  const baseBottom = POS_MAP[style.position];
  return {
    bottom: uiOpen ? `max(${baseBottom}, 82px)` : baseBottom,
  };
}

export function cueLineStyle(style: SubtitleStyle, preview = false): CSSProperties {
  const color = SUB_COLORS[style.color].hex;
  const fontSize = preview
    ? style.size === "sm"
      ? "13px"
      : style.size === "md"
        ? "16px"
        : style.size === "lg"
          ? "20px"
          : "24px"
    : SIZE_MAP[style.size];

  const base: CSSProperties = {
    color,
    fontSize,
    fontFamily: FONT_MAP[style.font],
  };

  if (style.bg === "solid") {
    return {
      ...base,
      background: "rgba(6, 5, 4, 0.94)",
      padding: "0.18em 0.5em",
      borderRadius: "6px",
      textShadow: "none",
    };
  }
  if (style.bg === "outline") {
    return {
      ...base,
      background: "transparent",
      padding: "0.08em 0.3em",
      borderRadius: "4px",
      textShadow:
        "0 2px 4px rgba(0,0,0,0.95), 0 0 8px rgba(0,0,0,0.9), 1px 1px 0 #000, -1px -1px 0 #000, 1px -1px 0 #000, -1px 1px 0 #000",
    };
  }
  return {
    ...base,
    background: "rgba(8, 7, 6, 0.72)",
    padding: "0.18em 0.48em",
    borderRadius: "6px",
    textShadow: "0 1px 2px rgba(0,0,0,0.7)",
  };
}

export interface CueToken {
  text: string;
  italic?: boolean;
  bold?: boolean;
  underline?: boolean;
}

const NAMED_ENTITIES: Record<string, string> = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  apos: "'",
  nbsp: " ",
  ndash: "–",
  mdash: "—",
  hellip: "…",
  lsquo: "‘",
  rsquo: "’",
  ldquo: "“",
  rdquo: "”",
  copy: "©",
  lrm: "",
  rlm: "",
};

function decodeEntitiesOnce(s: string): string {
  return s.replace(/&(#x[0-9a-fA-F]+|#[0-9]+|[a-zA-Z]+);/g, (match, code: string) => {
    if (code.startsWith("#x") || code.startsWith("#X")) {
      const cp = parseInt(code.slice(2), 16);
      return Number.isFinite(cp) && cp > 0 && cp <= 0x10ffff ? String.fromCodePoint(cp) : match;
    }
    if (code.startsWith("#")) {
      const cp = parseInt(code.slice(1), 10);
      return Number.isFinite(cp) && cp > 0 && cp <= 0x10ffff ? String.fromCodePoint(cp) : match;
    }
    const named = NAMED_ENTITIES[code.toLowerCase()];
    return named !== undefined ? named : match;
  });
}

function decodeHtmlEntities(s: string): string {
  const first = decodeEntitiesOnce(s);
  return first.includes("&") ? decodeEntitiesOnce(first) : first;
}

// Decodes escaped HTML entities (like &#39;, &#34;, &lt;i&gt;), converts inline
// formatting (<i>, <b>, <u>) into safe tokens, and strips stray subtitle tags.
export function parseCueLines(raw: string): CueToken[][] {
  const decoded = decodeHtmlEntities(raw)
    .replace(/\r\n/g, "\n")
    .replace(/\r/g, "\n")
    .replace(/<br\s*\/?>/gi, "\n")
    .replace(/\{\\[^}]*\}/g, "")
    .replace(/<\d{1,2}:\d{2}:\d{2}\.\d{3}>/g, "");

  let italic = false;
  let bold = false;
  let underline = false;

  return decoded
    .split("\n")
    .map((line) => {
      const tokens: CueToken[] = [];
      const parts = line.split(/(<\/?[a-zA-Z][^>]*>)/g);
      for (const part of parts) {
        if (!part) continue;
        if (part.startsWith("<") && part.endsWith(">")) {
          const name = part.slice(1, -1).trim().split(/[\s.]/)[0].toLowerCase();
          if (name === "i") italic = true;
          else if (name === "/i") italic = false;
          else if (name === "b") bold = true;
          else if (name === "/b") bold = false;
          else if (name === "u") underline = true;
          else if (name === "/u") underline = false;
          // Strip other tags (<font>, <c>, <v>, <lang>, etc.)
        } else {
          tokens.push({ text: part, italic, bold, underline });
        }
      }
      return tokens;
    })
    .filter((tokens) => tokens.some((t) => t.text.trim() !== ""));
}
