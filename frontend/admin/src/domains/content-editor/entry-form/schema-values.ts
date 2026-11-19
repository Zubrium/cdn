import type { JsonObject, JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";

export function starterValue(field: SchemaFieldNode): JsonValue {
  if (field.defaultValue) {
    switch (field.type) {
      case "number":
      case "integer": {
        const parsed = Number(field.defaultValue);
        return Number.isFinite(parsed) ? parsed : 0;
      }
      case "boolean":
        return field.defaultValue === "true";
      default:
        return field.defaultValue;
    }
  }
  switch (field.type) {
    case "number":
    case "integer": {
      if (typeof field.min === "number") return field.min;
      if (typeof field.max === "number" && field.max < 0) return field.max;
      return 0;
    }
    case "array": {
      const minCount = Math.max(0, field.minItems ?? 0);
      const rows: JsonValue[] = [];
      for (let i = 0; i < minCount; i += 1) {
        if (field.itemType === "object") {
          const row: JsonObject = {};
          field.children.forEach((child) => {
            if (child.key.trim()) row[child.key.trim()] = starterValue(child);
          });
          rows.push(row);
          continue;
        }
        const pseudo: SchemaFieldNode = {
          ...field,
          id: `${field.id}-item`,
          key: "item",
          type: field.itemType as SchemaFieldNode["type"],
          children: [],
          required: false,
        };
        rows.push(pseudo.type === "object" ? {} : starterValue(pseudo));
      }
      return rows;
    }
    case "boolean":
      return false;
    case "enum":
      return field.enumValues.find((option) => option.value.trim())?.value.trim() ?? "";
    case "object": {
      const object: JsonObject = {};
      field.children.forEach((child) => {
        if (child.key.trim()) object[child.key.trim()] = starterValue(child);
      });
      return object;
    }
    case "date":
      return new Date().toISOString().slice(0, 10);
    case "dateTime":
      return new Date().toISOString();
    default:
      return "";
  }
}
