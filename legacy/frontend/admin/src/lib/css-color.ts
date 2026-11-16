export interface ParsedCssColor {
  hex: string;
  alpha: number;
}

const clamp = (value: number, min: number, max: number) =>
  Math.min(max, Math.max(min, value));

function toHex(red: number, green: number, blue: number): string {
  return `#${[red, green, blue]
    .map((value) => clamp(Math.round(value), 0, 255).toString(16).padStart(2, "0"))
    .join("")}`;
}

function parseAlpha(value: string | undefined): number {
  if (value === undefined) return 100;
  const numeric = Number.parseFloat(value);
  if (!Number.isFinite(numeric)) return 100;
  const percent = value.trim().endsWith("%") ? numeric : numeric * 100;
  return Math.round(clamp(percent, 0, 100));
}

function parseHexColor(value: string): ParsedCssColor | null {
  let hex = value.slice(1);
  if (![3, 4, 6, 8].includes(hex.length) || !/^[\da-f]+$/i.test(hex)) return null;
  if (hex.length === 3 || hex.length === 4) {
    hex = hex.split("").map((part) => part + part).join("");
  }
  if (hex.length === 8) {
    return {
      hex: `#${hex.slice(0, 6)}`,
      alpha: Math.round(Number.parseInt(hex.slice(6), 16) / 255 * 100),
    };
  }
  return { hex: `#${hex}`, alpha: 100 };
}

function splitFunctionalColor(value: string): { name: string; channels: string[]; alpha?: string } | null {
  const match = value.match(/^([a-z]+)\((.*)\)$/i);
  if (!match) return null;
  const name = match[1].toLowerCase();
  if (!["rgb", "rgba", "hsl", "hsla"].includes(name)) return null;

  const [channelText, slashAlpha] = match[2].split("/");
  const channels = channelText.includes(",")
    ? channelText.split(",").map((part) => part.trim())
    : channelText.trim().split(/\s+/);
  let alpha: string | undefined = slashAlpha?.trim();
  if (!alpha && channels.length === 4) alpha = channels.pop() ?? undefined;
  if (channels.length !== 3 || channels.some((channel) => channel === "")) return null;
  return { name, channels, alpha };
}

function rgbChannels(channels: string[]): [number, number, number] | null {
  const values = channels.map((channel) => {
    const numeric = Number.parseFloat(channel);
    if (!Number.isFinite(numeric)) return null;
    return channel.trim().endsWith("%") ? clamp(numeric, 0, 100) * 2.55 : clamp(numeric, 0, 255);
  });
  if (values.some((value) => value === null)) return null;
  return values as [number, number, number];
}

function hslChannels(channels: string[]): [number, number, number] | null {
  const [rawHue, rawSaturation, rawLightness] = channels;
  const hueValue = Number.parseFloat(rawHue);
  const saturationValue = Number.parseFloat(rawSaturation);
  const lightnessValue = Number.parseFloat(rawLightness);
  if (![hueValue, saturationValue, lightnessValue].every(Number.isFinite)) return null;

  const hueUnit = rawHue.trim().match(/(deg|rad|turn)$/i)?.[1]?.toLowerCase();
  const degrees = hueUnit === "rad"
    ? hueValue * 180 / Math.PI
    : hueUnit === "turn" ? hueValue * 360 : hueValue;
  const hue = ((degrees % 360) + 360) % 360;
  const saturation = clamp(saturationValue / 100, 0, 1);
  const lightness = clamp(lightnessValue / 100, 0, 1);
  const chroma = (1 - Math.abs(2 * lightness - 1)) * saturation;
  const sector = hue / 60;
  const x = chroma * (1 - Math.abs((sector % 2) - 1));
  const [red, green, blue] = sector < 1 ? [chroma, x, 0]
    : sector < 2 ? [x, chroma, 0]
      : sector < 3 ? [0, chroma, x]
        : sector < 4 ? [0, x, chroma]
          : sector < 5 ? [x, 0, chroma]
            : [chroma, 0, x];
  const match = lightness - chroma / 2;
  return [red, green, blue].map((channel) => (channel + match) * 255) as [number, number, number];
}

export function parseCssColor(value: string): ParsedCssColor | null {
  const normalized = value.trim();
  if (normalized.startsWith("#")) return parseHexColor(normalized);

  const functional = splitFunctionalColor(normalized);
  if (!functional) return null;
  const channels = functional.name.startsWith("hsl")
    ? hslChannels(functional.channels)
    : rgbChannels(functional.channels);
  if (!channels) return null;
  return {
    hex: toHex(...channels),
    alpha: parseAlpha(functional.alpha),
  };
}

export function cssColorValue(hex: string, alpha: number): string {
  const normalized = parseCssColor(hex)?.hex ?? "#ffffff";
  if (alpha >= 100) return normalized;
  const rgb = [1, 3, 5].map((offset) => Number.parseInt(normalized.slice(offset, offset + 2), 16));
  return `rgb(${rgb.join(" ")} / ${clamp(Math.round(alpha), 0, 100)}%)`;
}
