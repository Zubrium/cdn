const manifestKeys = new Set([
  "formatVersion", "key", "name", "category", "version", "engineApi", "entry", "styles",
  "sharedStyles", "runtime", "slots", "arguments", "dataInputs", "form", "designTokens", "requests",
]);
const inputTypes = new Set(["text", "longText", "integer", "number", "boolean", "date", "dateTime", "url", "email", "enum", "media"]);
const profileAccepts = new Set(["image", "raster-image", "video", "file"]);

function assertObject(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`${label} must be an object`);
}

function assertKeys(value, allowed, label) {
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) throw new Error(`${label} contains unsupported property ${JSON.stringify(key)}`);
  }
}

function validateRendition(value, label, expectedName) {
  assertObject(value, label);
  assertKeys(value, new Set(["name", "width", "height", "fit"]), label);
  if (typeof value.name !== "string" || !/^[a-z][a-z0-9_-]{0,31}$/.test(value.name)) throw new Error(`${label}.name is invalid`);
  if (expectedName && value.name !== expectedName) throw new Error(`${label}.name must be ${expectedName}`);
  for (const dimension of ["width", "height"]) {
    if (!Number.isInteger(value[dimension]) || value[dimension] < 1 || value[dimension] > 8192) throw new Error(`${label}.${dimension} must be an integer from 1 to 8192`);
  }
  if (value.fit !== "contain" && value.fit !== "cover") throw new Error(`${label}.fit must be contain or cover`);
}

function validateProfile(value, fieldType, label) {
  if (value === undefined) return;
  if (fieldType !== "media") throw new Error(`${label}.profile is valid only for media inputs`);
  assertObject(value, `${label}.profile`);
  assertKeys(value, new Set(["accept", "renditions", "video"]), `${label}.profile`);
  if (!profileAccepts.has(value.accept)) throw new Error(`${label}.profile.accept is unsupported`);
  if (value.accept === "video") {
    if (value.video === undefined) throw new Error(`${label}.profile.video is required for video inputs`);
    if (value.renditions !== undefined) throw new Error(`${label}.profile.renditions cannot be combined with video`);
    assertObject(value.video, `${label}.profile.video`);
    assertKeys(value.video, new Set(["maxWidth", "maxHeight", "poster"]), `${label}.profile.video`);
    for (const dimension of ["maxWidth", "maxHeight"]) {
      if (!Number.isInteger(value.video[dimension]) || value.video[dimension] < 1 || value.video[dimension] > 8192) throw new Error(`${label}.profile.video.${dimension} must be an integer from 1 to 8192`);
    }
    validateRendition(value.video.poster, `${label}.profile.video.poster`, "poster");
    return;
  }
  if (value.video !== undefined) throw new Error(`${label}.profile.video is only valid for video inputs`);
  if (value.renditions !== undefined) {
    if (value.accept !== "raster-image" || !Array.isArray(value.renditions)) throw new Error(`${label}.profile.renditions require raster-image and an array`);
    const names = new Set();
    for (const [index, rendition] of value.renditions.entries()) {
      validateRendition(rendition, `${label}.profile.renditions[${index}]`);
      if (names.has(rendition.name)) throw new Error(`${label}.profile.renditions contains duplicate name ${rendition.name}`);
      names.add(rendition.name);
    }
  }
}

function validateTypedValue(value, label) {
  if (value.type === "enum") {
    if (!Array.isArray(value.values) || value.values.length === 0 || value.values.some((item) => typeof item !== "string" || !item.trim()) || new Set(value.values).size !== value.values.length) {
      throw new Error(`${label}.values must contain unique, non-empty enum values`);
    }
  } else if (value.values !== undefined) {
    throw new Error(`${label}.values is only valid for enum types`);
  }
  validateProfile(value.profile, value.type, label);
}

function decodePath(path, label) {
  if (typeof path !== "string" || !path.startsWith("/")) throw new Error(`${label} must be a non-empty JSON Pointer`);
  const raw = path.slice(1).split("/");
  if (raw.some((part) => part === "" || /~(?![01])/.test(part))) throw new Error(`${label} must be a valid non-empty JSON Pointer`);
  const parts = raw.map((part) => part.replace(/~1/g, "/").replace(/~0/g, "~"));
  for (const [index, part] of parts.entries()) {
    if (part === "*") {
      if (index === 0 || index === parts.length - 1 || parts[index - 1] === "*") throw new Error(`${label} has an invalid array wildcard`);
    } else if (!/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(part)) {
      throw new Error(`${label} contains an invalid property name`);
    }
  }
  return parts;
}

function pathsOverlap(left, right) {
  const shorter = left.length <= right.length ? left : right;
  const longer = left.length <= right.length ? right : left;
  return shorter.every((part, index) => part === longer[index]);
}

export function validateDataInputManifest(manifest) {
  assertObject(manifest, "manifest");
  assertKeys(manifest, manifestKeys, "manifest");
  if (manifest.formatVersion !== 2) throw new Error("only manifest formatVersion 2 is supported");
  if (!Array.isArray(manifest.dataInputs)) throw new Error("dataInputs must be an array, including [] when the feature consumes no model data");
  const keys = new Set();
  for (const [index, input] of manifest.dataInputs.entries()) {
    const label = `dataInputs[${index}]`;
    assertObject(input, label);
    if (typeof input.key !== "string" || !/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(input.key) || keys.has(input.key)) throw new Error(`${label}.key must be a unique input key`);
    keys.add(input.key);
    if (input.kind === "scalar") {
      assertKeys(input, new Set(["key", "kind", "type", "values", "profile"]), label);
      if (!inputTypes.has(input.type)) throw new Error(`${label}.type must be a supported scalar data type`);
      validateTypedValue(input, label);
      continue;
    }
    if (input.kind !== "record" && input.kind !== "collection") throw new Error(`${label}.kind must be scalar, record, or collection`);
    assertKeys(input, new Set(["key", "kind", "fields"]), label);
    if (!Array.isArray(input.fields) || input.fields.length === 0) throw new Error(`${label}.fields must contain at least one typed field`);
    const paths = [];
    for (const [fieldIndex, field] of input.fields.entries()) {
      const fieldLabel = `${label}.fields[${fieldIndex}]`;
      assertObject(field, fieldLabel);
      assertKeys(field, new Set(["path", "type", "values", "profile"]), fieldLabel);
      const parts = decodePath(field.path, `${fieldLabel}.path`);
      if (paths.some(({ source, parts: existing }) => source === field.path || pathsOverlap(existing, parts))) {
        throw new Error(`${label} contains duplicate or overlapping field path ${field.path}`);
      }
      paths.push({ source: field.path, parts });
      if (!inputTypes.has(field.type)) throw new Error(`${fieldLabel}.type must be a supported data type`);
      validateTypedValue(field, fieldLabel);
    }
  }
}
