const htmlTags = new Set([
  "a", "abbr", "address", "article", "aside", "b", "blockquote", "br", "button", "caption", "cite", "code", "dd", "del", "details", "div", "dl", "dt", "em", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "i", "img", "ins", "kbd", "li", "main", "mark", "ol", "p", "picture", "pre", "q", "s", "samp", "section", "small", "source", "span", "strong", "sub", "summary", "sup", "table", "tbody", "td", "tfoot", "th", "thead", "time", "tr", "u", "ul", "video", "audio", "track",
]);

const svgTags = new Set([
  "svg", "g", "path", "circle", "ellipse", "rect", "line", "polyline", "polygon", "title", "desc", "defs", "use", "clippath", "mask", "lineargradient", "radialgradient", "stop", "symbol",
]);

const globalAttributes = new Set([
  "id", "class", "title", "role", "lang", "dir", "tabindex", "hidden", "draggable",
]);

const svgAttributes = new Set([
  "viewbox", "xmlns", "fill", "fill-rule", "stroke", "stroke-width", "stroke-linecap", "stroke-linejoin", "stroke-dasharray", "stroke-dashoffset", "opacity", "d", "cx", "cy", "r", "rx", "ry", "x", "x1", "x2", "y", "y1", "y2", "width", "height", "points", "transform", "preserveaspectratio", "offset", "stop-color", "stop-opacity", "gradientunits", "gradienttransform", "clip-rule", "focusable",
]);

const templateExpression = /\{\{[\s\S]*?\}\}|\$\{[\s\S]*?\}|<%[\s\S]*?%>|\{%[\s\S]*?%\}/;

export function parseStaticHTMLFragment(source: string, entryUrl: string): DocumentFragment {
  if (templateExpression.test(source)) throw new Error("HTML templates are not supported");

  const entry = new URL(entryUrl, window.location.origin);
  if (entry.origin !== window.location.origin) throw new Error("Feature HTML must be served from this site");
  const buildPrefix = entry.pathname.match(/^\/feature-assets\/[a-f0-9]{64}\//i)?.[0];
  if (!buildPrefix) throw new Error("Invalid feature asset URL");

  const template = document.createElement("template");
  template.innerHTML = source;
  const elements = template.content.querySelectorAll("*");

  for (const element of elements) {
    const tag = element.localName.toLowerCase();
    const isSVG = element.namespaceURI === "http://www.w3.org/2000/svg";
    if (element.namespaceURI !== "http://www.w3.org/1999/xhtml" && !isSVG) {
      throw new Error("Foreign HTML content is not supported");
    }
    if (isSVG ? !svgTags.has(tag) : !htmlTags.has(tag)) {
      throw new Error(`HTML element <${tag}> is not allowed`);
    }

    for (const attribute of [...element.attributes]) {
      const name = attribute.name.toLowerCase();
      if (name.startsWith("on") || ["style", "srcdoc", "action", "formaction", "form"].includes(name)) {
        throw new Error(`HTML attribute ${name} is not allowed`);
      }

      if (name.startsWith("aria-") || name.startsWith("data-") || globalAttributes.has(name) || isSafeAttribute(tag, name)) {
        if (name === "xmlns" && attribute.value !== "http://www.w3.org/2000/svg") {
          throw new Error("Invalid SVG namespace");
        }
        if (name === "href") {
          if (isSVG) {
            if (!attribute.value.trim().startsWith("#")) throw new Error("SVG links must refer to a local fragment");
          } else {
            validateLink(attribute.value);
          }
        }
        if (name === "src" || name === "poster") {
          element.setAttribute(attribute.name, resolveFeatureResource(attribute.value, entry, buildPrefix));
        }
        if (name === "srcset") {
          element.setAttribute(attribute.name, resolveSrcset(attribute.value, entry, buildPrefix));
        }
        if (isSVG && ["fill", "stroke", "filter", "clip-path", "mask"].includes(name) && attribute.value.toLowerCase().includes("url(")) {
          const value = attribute.value.trim();
          if (!/^url\(\s*#[A-Za-z0-9_.:-]+\s*\)$/i.test(value)) {
            throw new Error("SVG paint and filter URLs must refer to a local fragment");
          }
        }
        continue;
      }
      if (isSVG && svgAttributes.has(name)) continue;
      throw new Error(`HTML attribute ${name} is not allowed on <${tag}>`);
    }

    if (!isSVG && tag === "a" && element.getAttribute("target")?.toLowerCase() === "_blank") {
      const rel = new Set((element.getAttribute("rel") ?? "").toLowerCase().split(/\s+/));
      if (!rel.has("noopener") && !rel.has("noreferrer")) {
        throw new Error('Links with target="_blank" need rel="noopener" or rel="noreferrer"');
      }
    }
  }

  return template.content;
}

function isSafeAttribute(tag: string, name: string): boolean {
  if (name === "href") return tag === "a" || tag === "area" || tag === "use";
  if (name === "srcset") return tag === "img" || tag === "source";
  switch (tag) {
    case "img": return ["alt", "width", "height", "loading", "decoding", "src"].includes(name);
    case "source": return ["src", "type", "media", "sizes"].includes(name);
    case "video": return ["src", "poster", "controls", "loop", "muted", "preload", "playsinline", "width", "height"].includes(name);
    case "audio": return ["src", "controls", "loop", "muted", "preload"].includes(name);
    case "track": return ["src", "kind", "srclang", "label", "default"].includes(name);
    case "button": return ["type", "disabled"].includes(name);
    case "details": return name === "open";
    case "td":
    case "th": return ["colspan", "rowspan", "headers", "scope"].includes(name);
    case "blockquote":
    case "q": return name === "cite";
    default: return false;
  }
}

function hasControlCharacters(value: string): boolean {
  for (const character of value) {
    if (character.charCodeAt(0) <= 0x1f) return true;
  }
  return false;
}

function validateLink(raw: string): void {
  const value = raw.trim();
  if (!value || hasControlCharacters(value)) throw new Error("Link URL is empty or contains a control character");
  const parsed = new URL(value, window.location.href);
  if (!["http:", "https:", "mailto:", "tel:"].includes(parsed.protocol)) {
    throw new Error(`Link URL scheme ${parsed.protocol} is not allowed`);
  }
  if (parsed.username || parsed.password) throw new Error("Links with embedded credentials are not allowed");
}

function resolveFeatureResource(raw: string, entry: URL, buildPrefix: string): string {
  const value = raw.trim();
  if (!value || value.includes("\\") || hasControlCharacters(value)) throw new Error("Feature resource URL is invalid");
  const resolved = new URL(value, entry);
  if (resolved.origin !== entry.origin || !resolved.pathname.startsWith(buildPrefix) || resolved.search || resolved.hash) {
    throw new Error("Feature resources must be relative files in the uploaded ZIP");
  }
  return resolved.href;
}

function resolveSrcset(raw: string, entry: URL, buildPrefix: string): string {
  return raw.split(",").map((candidate) => {
    const [url, descriptor, ...rest] = candidate.trim().split(/\s+/);
    if (!url || rest.length > 0 || (descriptor && !/^(?:[1-9][0-9]*(?:\.[0-9]+)?x|[1-9][0-9]*w)$/.test(descriptor))) {
      throw new Error("Feature srcset is invalid");
    }
    const resolved = resolveFeatureResource(url, entry, buildPrefix);
    return descriptor ? `${resolved} ${descriptor}` : resolved;
  }).join(", ");
}
